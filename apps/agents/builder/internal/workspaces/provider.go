package workspaces

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/defos-ai/commander/framework/domain/sessions"
)

type Workspace struct {
	ID       string
	Revision string
	root     string
}

type record struct {
	claim     sessions.CreationClaim
	session   sessions.Session
	workspace Workspace
	closing   bool
	cancel    context.CancelFunc
	done      chan struct{}
}

type Provider struct {
	root    string
	mu      sync.Mutex
	records map[string]*record
	changes chan struct{}
	stopped bool
}

func New(directory string) (*Provider, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, errors.New("workspace directory must not be blank")
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("workspace directory: %w", err)
	}
	return &Provider{root: root, records: make(map[string]*record), changes: make(chan struct{}, 1)}, nil
}

func (p *Provider) Prepare(ctx context.Context, claim sessions.CreationClaim) error {
	if err := claim.Validate(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.stopped {
		return errors.New("builder workspace owner stopped")
	}
	if previous := p.records[claim.ID]; previous != nil {
		if previous.claim != claim {
			return sessions.ErrConflict
		}
		if previous.closing || previous.session.State == "closed" {
			return sessions.ErrSessionClosed
		}
		return nil
	}
	p.records[claim.ID] = newRecord(claim)
	p.notify()
	return nil
}

func newRecord(claim sessions.CreationClaim) *record {
	now := time.Now().UTC()
	return &record{claim: claim, session: sessions.Session{
		Reference: claim.Reference, Kind: "workspace", Label: "Builder workspace", State: "starting",
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}}
}

func (p *Provider) Snapshot() []sessions.Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	items := make([]sessions.Session, 0, len(p.records))
	for _, record := range p.records {
		items = append(items, record.session.Clone())
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ScopeID != items[j].ScopeID {
			return items[i].ScopeID < items[j].ScopeID
		}
		return items[i].ID < items[j].ID
	})
	return items
}

func (p *Provider) Changes() <-chan struct{} { return p.changes }

func (p *Provider) notify() {
	select {
	case p.changes <- struct{}{}:
	default:
	}
}

func (p *Provider) changed(item *record) {
	item.session.Revision++
	item.session.UpdatedAt = time.Now().UTC()
	p.notify()
}

func (p *Provider) Checkout(ctx context.Context, binding sessions.Binding, clone func(context.Context, string) (string, error)) (workspace Workspace, observed sessions.Session, err error) {
	if binding.Mode != sessions.Open {
		return Workspace{}, sessions.Session{}, sessions.ErrConflict
	}
	ctx, item, release, err := p.acquire(ctx, binding, false)
	if err != nil {
		return Workspace{}, sessions.Session{}, err
	}
	defer release()
	if item.workspace.ID != "" {
		if _, err := item.workspace.Source(); err != nil {
			return Workspace{}, sessions.Session{}, err
		}
		return item.workspace, p.observation(item), nil
	}
	if err := os.MkdirAll(p.root, 0700); err != nil {
		return Workspace{}, sessions.Session{}, errors.New("create builder workspace directory failed")
	}
	info, err := os.Lstat(p.root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Workspace{}, sessions.Session{}, errors.New("builder workspace directory is unavailable or unsafe")
	}
	directory, err := os.MkdirTemp(p.root, "checkout-")
	if err != nil {
		return Workspace{}, sessions.Session{}, errors.New("create checkout workspace failed")
	}
	retained := false
	defer func() {
		if !retained {
			if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
				err = errors.Join(err, errors.New("remove incomplete checkout failed"))
			}
		}
	}()
	revision, err := clone(ctx, directory)
	if err != nil {
		return Workspace{}, sessions.Session{}, err
	}
	workspace = Workspace{ID: filepath.Base(directory), Revision: revision, root: p.root}
	if _, err := workspace.Source(); err != nil {
		return Workspace{}, sessions.Session{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Workspace{}, sessions.Session{}, err
	}
	if item.closing || p.stopped {
		return Workspace{}, sessions.Session{}, sessions.ErrSessionClosed
	}
	now := time.Now().UTC()
	item.workspace = workspace
	item.session.State, item.session.ConnectedAt, item.session.VerifiedAt = "connected", &now, &now
	p.changed(item)
	retained = true
	return workspace, item.session.Clone(), nil
}

func (p *Provider) Use(ctx context.Context, binding sessions.Binding) (context.Context, Workspace, func(), error) {
	if binding.Mode != sessions.Use {
		return nil, Workspace{}, nil, sessions.ErrConflict
	}
	ctx, item, release, err := p.acquire(ctx, binding, true)
	if err != nil {
		return nil, Workspace{}, nil, err
	}
	if _, err := item.workspace.Source(); err != nil {
		release()
		return nil, Workspace{}, nil, err
	}
	return ctx, item.workspace, release, nil
}

func (p *Provider) acquire(ctx context.Context, binding sessions.Binding, connected bool) (context.Context, *record, func(), error) {
	if err := binding.Validate(); err != nil {
		return nil, nil, nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	item := p.records[binding.ID]
	if item == nil {
		return nil, nil, nil, sessions.ErrUnknownSession
	}
	if item.claim != binding.Claim() {
		return nil, nil, nil, sessions.ErrConflict
	}
	if p.stopped || item.closing || item.session.State == "closed" {
		return nil, nil, nil, sessions.ErrSessionClosed
	}
	if item.done != nil {
		return nil, nil, nil, errors.New("builder workspace is busy")
	}
	if connected && (item.session.State != "connected" || item.workspace.ID == "") {
		return nil, nil, nil, errors.New("builder workspace is not connected")
	}
	ctx, item.cancel = context.WithCancel(ctx)
	item.done = make(chan struct{})
	item.session.Busy = true
	p.changed(item)
	return ctx, item, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		item.cancel()
		item.cancel = nil
		item.session.Busy = false
		p.changed(item)
		close(item.done)
		item.done = nil
	}, nil
}

func (p *Provider) observation(item *record) sessions.Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	return item.session.Clone()
}

func (w Workspace) Source() (string, error) {
	info, err := os.Lstat(w.root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("builder workspace directory is unavailable or unsafe")
	}
	root, err := os.OpenRoot(w.root)
	if err != nil {
		return "", errors.New("builder workspace directory is unavailable")
	}
	defer root.Close()
	for _, name := range []string{w.ID, filepath.Join(w.ID, "source")} {
		info, err := root.Lstat(name)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("workspace is missing or unsafe; use its owning builder session")
		}
	}
	return filepath.Join(w.root, w.ID, "source"), nil
}

var _ sessions.Provider = (*Provider)(nil)
