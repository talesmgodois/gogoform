package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5"

	"app/internal/database"
	"app/internal/db"
	apperrors "app/internal/errors"
)

// Client-facing messages of the errors returned by Service.
const (
	msgNotFound       = "file not found"
	msgTenantNotFound = "tenant not found"
	msgInvalidPage    = "page offset must be >= 0 and limit must be > 0"
)

// uuidPattern is the canonical textual form of a UUID. IDs that do not match
// cannot exist, so they are reported as not found without querying (Postgres
// would reject them with an invalid-input error).
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var _ Repository = (*Service)(nil)

// Service implements Repository on top of the sqlc queries.
type Service struct {
	q db.Querier
}

// NewService returns a Service that runs its queries through q.
func NewService(q db.Querier) *Service {
	return &Service{q: q}
}

// Create stores a new file and returns it without its Data. The size and
// checksum are computed from in.Data.
func (s *Service) Create(ctx context.Context, in CreateFileInput) (File, error) {
	sum := sha256.Sum256(in.Data)
	row, err := s.q.CreateFile(ctx, db.CreateFileParams{
		TenantID:       in.TenantID,
		Name:           in.Name,
		ContentType:    in.ContentType,
		SizeBytes:      int64(len(in.Data)),
		ChecksumSha256: hex.EncodeToString(sum[:]),
		Data:           in.Data,
	})
	switch {
	case database.IsForeignKeyViolation(err):
		return File{}, apperrors.NewNotFound(msgTenantNotFound)
	case err != nil:
		return File{}, apperrors.NewInternal(err)
	}
	return File{
		ID:          row.ID,
		TenantID:    row.TenantID,
		Name:        row.Name,
		ContentType: row.ContentType,
		Size:        row.SizeBytes,
		Checksum:    row.ChecksumSha256,
		CreatedAt:   row.CreatedAt,
	}, nil
}

// GetByID returns the file with the given ID, including its Data.
func (s *Service) GetByID(ctx context.Context, id string) (File, error) {
	if !uuidPattern.MatchString(id) {
		return File{}, apperrors.NewNotFound(msgNotFound)
	}
	row, err := s.q.GetFileByID(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return File{}, apperrors.NewNotFound(msgNotFound)
	case err != nil:
		return File{}, apperrors.NewInternal(err)
	}
	return File{
		ID:          row.ID,
		TenantID:    row.TenantID,
		Name:        row.Name,
		ContentType: row.ContentType,
		Size:        row.SizeBytes,
		Checksum:    row.ChecksumSha256,
		CreatedAt:   row.CreatedAt,
		Data:        row.Data,
	}, nil
}

// ListAll returns the files of every tenant without their Data, newest first.
func (s *Service) ListAll(ctx context.Context, page Page) ([]FileSummary, error) {
	if page.Offset < 0 || page.Limit < 1 {
		return nil, apperrors.NewBadRequest(msgInvalidPage)
	}
	rows, err := s.q.ListAllFiles(ctx, db.ListAllFilesParams{PageOffset: page.Offset, PageLimit: page.Limit})
	if err != nil {
		return nil, apperrors.NewInternal(err)
	}
	files := make([]FileSummary, len(rows))
	for i, row := range rows {
		files[i] = FileSummary{
			File: File{
				ID:          row.ID,
				TenantID:    row.TenantID,
				Name:        row.Name,
				ContentType: row.ContentType,
				Size:        row.SizeBytes,
				Checksum:    row.ChecksumSha256,
				CreatedAt:   row.CreatedAt,
			},
			TenantName: row.TenantName,
		}
	}
	return files, nil
}

// CountAll returns how many files exist across every tenant.
func (s *Service) CountAll(ctx context.Context) (int64, error) {
	n, err := s.q.CountAllFiles(ctx)
	if err != nil {
		return 0, apperrors.NewInternal(err)
	}
	return n, nil
}

// Delete removes the tenant's file with the given ID.
func (s *Service) Delete(ctx context.Context, tenantID int32, id string) error {
	if !uuidPattern.MatchString(id) {
		return apperrors.NewNotFound(msgNotFound)
	}
	n, err := s.q.DeleteFile(ctx, db.DeleteFileParams{ID: id, TenantID: tenantID})
	switch {
	case err != nil:
		return apperrors.NewInternal(err)
	case n == 0:
		return apperrors.NewNotFound(msgNotFound)
	}
	return nil
}
