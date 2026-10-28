package reports

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/id"
)

// Filesystems must support hard links: publishing a synced temporary file makes report
// creation atomic and exclusive across independent instances and processes.
type ReportFsStore struct{ root string }

var _ reports.ReportStore = (*ReportFsStore)(nil)

func NewReportFsStore(root string) (*ReportFsStore, error) {
	if root == "" {
		root = filepath.Join("state", "storage")
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("report storage root must not be blank")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("report storage root: %w", err)
	}
	return &ReportFsStore{root: absolute}, nil
}

func (s *ReportFsStore) Root() string { return s.root }

func (s *ReportFsStore) Put(ctx context.Context, report reports.Report) error {
	if err := checkReportContext(ctx); err != nil {
		return err
	}
	if err := report.Validate(); err != nil {
		return err
	}
	data, err := encodeReport(report)
	if err != nil {
		return err
	}
	directory, err := s.directory(true)
	if err != nil {
		return err
	}
	defer directory.Close()
	name := report.ID + ".json"
	if current, err := readReport(ctx, directory, report.ID); err == nil {
		return sameReport(directory, current, data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	temporary := ".pending-" + id.New()
	file, err := directory.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer directory.Remove(temporary)
	defer file.Close()
	if _, err := io.Copy(file, reportContextReader{ctx, bytes.NewReader(data)}); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := directory.Link(temporary, name); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		current, err := readReport(ctx, directory, report.ID)
		if err != nil {
			return err
		}
		return sameReport(directory, current, data)
	}
	// Publication has committed. A cancellation after this point must not turn
	// a successfully persisted report into a cancellation failure. A failed sync
	// may leave a report behind; an identical retry safely completes the sync.
	return syncReportDirectory(directory)
}

func (s *ReportFsStore) Get(ctx context.Context, reportID string) (reports.Report, error) {
	if err := checkReportContext(ctx); err != nil {
		return reports.Report{}, err
	}
	if !id.Valid(reportID) {
		return reports.Report{}, errors.New("report ID must be a canonical UUID")
	}
	directory, err := s.directory(false)
	if errors.Is(err, os.ErrNotExist) {
		return reports.Report{}, reports.ErrUnknownReport
	}
	if err != nil {
		return reports.Report{}, err
	}
	defer directory.Close()
	report, err := readReport(ctx, directory, reportID)
	if errors.Is(err, os.ErrNotExist) {
		return reports.Report{}, reports.ErrUnknownReport
	}
	return report, err
}

func (s *ReportFsStore) directory(create bool) (*os.Root, error) {
	info, err := os.Lstat(s.root)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := os.MkdirAll(s.root, 0700); err != nil {
			return nil, err
		}
		info, err = os.Lstat(s.root)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("report storage root must be a real directory")
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, errors.New("report storage root changed while opening")
	}
	info, err = root.Lstat("reports")
	if errors.Is(err, os.ErrNotExist) && create {
		if err := root.Mkdir("reports", 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		info, err = root.Lstat("reports")
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("reports path must be a real directory")
	}
	directory, err := root.OpenRoot("reports")
	if err != nil {
		return nil, err
	}
	opened, err = directory.Stat(".")
	if err == nil && !os.SameFile(info, opened) {
		err = errors.New("reports directory changed while opening")
	}
	if err != nil {
		directory.Close()
		return nil, err
	}
	return directory, nil
}

func readReport(ctx context.Context, directory *os.Root, reportID string) (reports.Report, error) {
	if err := ctx.Err(); err != nil {
		return reports.Report{}, err
	}
	name := reportID + ".json"
	info, err := directory.Lstat(name)
	if err != nil {
		return reports.Report{}, err
	}
	if !info.Mode().IsRegular() {
		return reports.Report{}, errors.New("report path must be a regular file, not a symlink or directory")
	}
	file, err := directory.Open(name)
	if err != nil {
		return reports.Report{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err == nil && (!opened.Mode().IsRegular() || !os.SameFile(info, opened)) {
		err = errors.New("report file changed while opening")
	}
	if err != nil {
		return reports.Report{}, err
	}
	data, err := io.ReadAll(reportContextReader{ctx, file})
	if err != nil {
		return reports.Report{}, err
	}
	if !utf8.Valid(data) {
		return reports.Report{}, errors.New("report file must contain valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var report reports.Report
	if err := decoder.Decode(&report); err != nil {
		return reports.Report{}, fmt.Errorf("decode report: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err != nil {
			return reports.Report{}, fmt.Errorf("decode report trailing data: %w", err)
		}
		return reports.Report{}, errors.New("report file must contain one JSON object")
	}
	if err := report.Validate(); err != nil {
		return reports.Report{}, fmt.Errorf("decode report: %w", err)
	}
	if report.ID != reportID {
		return reports.Report{}, errors.New("report ID does not match its filename")
	}
	if err := ctx.Err(); err != nil {
		return reports.Report{}, err
	}
	return report, nil
}

func encodeReport(report reports.Report) ([]byte, error) {
	report.CreatedAt = report.CreatedAt.UTC()
	data, err := json.Marshal(report)
	return append(data, '\n'), err
}

func sameReport(directory *os.Root, current reports.Report, data []byte) error {
	stored, err := encodeReport(current)
	if err != nil {
		return err
	}
	if !bytes.Equal(stored, data) {
		return reports.ErrReportConflict
	}
	return syncReportDirectory(directory)
}

func syncReportDirectory(directory *os.Root) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	file, err := directory.Open(".")
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func checkReportContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("report store requires a context")
	}
	return ctx.Err()
}

type reportContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r reportContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
