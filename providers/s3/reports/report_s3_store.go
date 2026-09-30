package reports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"commander/framework/domain/reports"
	"commander/framework/id"
	"commander/providers/s3/store"
)

type ReportS3Store struct{ storage *store.S3Store }

var _ reports.ReportStore = (*ReportS3Store)(nil)

func NewReportS3Store(storage *store.S3Store) (*ReportS3Store, error) {
	if storage == nil {
		return nil, errors.New("report S3 storage is required")
	}
	return &ReportS3Store{storage: storage}, nil
}

func (s *ReportS3Store) Put(ctx context.Context, report reports.Report) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if err := report.Validate(); err != nil {
		return err
	}
	data, err := encode(report)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	err = s.storage.Put(ctx, reportKey(report.ID), bytes.NewReader(data), int64(len(data)), "application/json", hex.EncodeToString(digest[:]))
	if !errors.Is(err, store.ErrObjectExists) {
		return err
	}
	current, err := s.Get(ctx, report.ID)
	if err != nil {
		return err
	}
	stored, err := encode(current)
	if err != nil {
		return err
	}
	if !bytes.Equal(stored, data) {
		return reports.ErrReportConflict
	}
	return nil
}

func (s *ReportS3Store) Get(ctx context.Context, reportID string) (reports.Report, error) {
	if err := checkContext(ctx); err != nil {
		return reports.Report{}, err
	}
	if !id.Valid(reportID) {
		return reports.Report{}, errors.New("report ID must be a canonical UUID")
	}
	data, err := s.storage.Get(ctx, reportKey(reportID))
	if errors.Is(err, store.ErrObjectNotFound) {
		return reports.Report{}, reports.ErrUnknownReport
	}
	if err != nil {
		return reports.Report{}, err
	}
	if !utf8.Valid(data) {
		return reports.Report{}, errors.New("stored report must contain valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var report reports.Report
	if err := decoder.Decode(&report); err != nil {
		return reports.Report{}, fmt.Errorf("decode report: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return reports.Report{}, errors.New("stored report must contain one JSON object")
	}
	if err := report.Validate(); err != nil {
		return reports.Report{}, fmt.Errorf("invalid stored report: %w", err)
	}
	if report.ID != reportID {
		return reports.Report{}, errors.New("stored report ID does not match its key")
	}
	return report, nil
}

func reportKey(reportID string) string { return "reports/" + reportID + ".json" }

func encode(report reports.Report) ([]byte, error) {
	report.CreatedAt = report.CreatedAt.UTC()
	data, err := json.Marshal(report)
	return append(data, '\n'), err
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("report store requires a context")
	}
	return ctx.Err()
}
