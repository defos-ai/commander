package actions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const maxBodyBytes = 2 << 20
const maxLinks = 40

func parseURL(raw string) (*url.URL, error) {
	if raw != strings.TrimSpace(raw) || len(raw) > 2048 {
		return nil, errors.New("url must be at most 2048 characters with no surrounding whitespace")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("url must be an absolute HTTP(S) URL without credentials")
	}
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u, nil
}

// Website scope follows an exact hostname across schemes and ports. A sibling
// or child subdomain is a different hostname, even when it resolves to the same IP.
func sameHostname(a, b *url.URL) bool {
	return a != nil && b != nil && a.Hostname() != "" && strings.EqualFold(a.Hostname(), b.Hostname())
}

func httpClient(ctx context.Context, scope *url.URL) (*http.Client, error) {
	resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	policy, err := resolveNetwork(resolveCtx, scope.Hostname(), lookupWebsite)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: policy.transport(), Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("redirect limit reached")
		}
		if req.URL.User != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
			return errors.New("redirect must use an HTTP(S) URL without credentials")
		}
		if !sameHostname(req.URL, scope) {
			return fmt.Errorf("redirect to %q leaves website scope %q; operator approval is required before reviewing hostname %q", req.URL.String(), scope.Hostname(), req.URL.Hostname())
		}
		return nil
	}}, nil
}

type pageDocument struct {
	URL     *url.URL
	Title   string
	Status  int
	Links   []string
	HTML    bool
	Root    *html.Node
	Headers http.Header
}

func readPage(ctx context.Context, client *http.Client, target *url.URL) (pageDocument, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return pageDocument{}, err
	}
	req.Header.Set("User-Agent", "Commander-Web-Agent/1.0")
	// client must come from httpClient, which pins validated IPs and restricts
	// redirect hostnames. Private targets are intentionally supported for reviews.
	response, err := client.Do(req)
	if err != nil {
		return pageDocument{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil {
		return pageDocument{}, err
	}
	if len(data) > maxBodyBytes {
		return pageDocument{}, errors.New("page exceeds the 2 MiB response limit")
	}
	finalURL, err := parseURL(response.Request.URL.String())
	if err != nil {
		return pageDocument{}, err
	}
	page := pageDocument{URL: finalURL, Status: response.StatusCode, Headers: response.Header.Clone()}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	page.HTML = strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml+xml")
	if !page.HTML {
		return page, nil
	}
	root, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return pageDocument{}, fmt.Errorf("parse page: %w", err)
	}
	page.Root = root
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode {
			if node.Data == "title" && node.FirstChild != nil {
				page.Title = strings.TrimSpace(node.FirstChild.Data)
			}
			if node.Data == "a" {
				for _, a := range node.Attr {
					if a.Key == "href" {
						page.Links = append(page.Links, a.Val)
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	if len(page.Title) > 300 {
		page.Title = string([]rune(page.Title)[:min(300, len([]rune(page.Title)))])
	}
	return page, nil
}

func resolveLink(base *url.URL, raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return parseURL(base.ResolveReference(u).String())
}
