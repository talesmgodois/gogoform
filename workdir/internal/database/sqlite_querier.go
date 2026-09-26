package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"app/internal/db"
	"app/internal/db/sqlitegen"
)

// sqliteQuerier adapts the sqlc-generated sqlitegen.Queries (built on
// database/sql) to the engine-neutral db.Querier interface, converting
// SQLite's int64/string-only types to the int32/db.UserRole types the rest
// of the app expects.
type sqliteQuerier struct {
	q *sqlitegen.Queries
}

// newSQLiteQuerier wraps dbtx (typically a *sql.DB) as a db.Querier.
func newSQLiteQuerier(dbtx sqlitegen.DBTX) *sqliteQuerier {
	return &sqliteQuerier{q: sqlitegen.New(dbtx)}
}

// mapSQLiteErr translates database/sql's no-rows sentinel to the
// engine-neutral ErrNoRows. Every other error, including *sqlite.Error, is
// passed through unchanged so the violation helpers in errors.go can still
// inspect it.
func mapSQLiteErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoRows
	}
	return err
}

// toInt32 narrows a SQLite int64 id to the int32 the rest of the app uses.
// It errors instead of silently truncating on overflow.
func toInt32(id int64) (int32, error) {
	if id < math.MinInt32 || id > math.MaxInt32 {
		return 0, fmt.Errorf("database: id %d out of int32 range", id)
	}
	return int32(id), nil
}

// toInt32Ptr narrows an optional SQLite int64 id to *int32, leaving nil as
// nil.
func toInt32Ptr(id *int64) (*int32, error) {
	if id == nil {
		return nil, nil
	}
	v, err := toInt32(*id)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// fromInt32Ptr widens an optional int32 id to the int64 SQLite expects.
func fromInt32Ptr(id *int32) *int64 {
	if id == nil {
		return nil
	}
	v := int64(*id)
	return &v
}

func toTenant(t sqlitegen.Tenant) (db.Tenant, error) {
	id, err := toInt32(t.ID)
	if err != nil {
		return db.Tenant{}, err
	}
	return db.Tenant{
		ID:        id,
		Name:      t.Name,
		ApiKey:    t.ApiKey,
		IsActive:  t.IsActive,
		CreatedAt: t.CreatedAt,
	}, nil
}

func toUser(u sqlitegen.User) (db.User, error) {
	id, err := toInt32(u.ID)
	if err != nil {
		return db.User{}, err
	}
	return db.User{
		ID:           id,
		Username:     u.Username,
		PasswordHash: u.PasswordHash,
		Role:         db.UserRole(u.Role),
		IsActive:     u.IsActive,
		CreatedAt:    u.CreatedAt,
		UpdatedAt:    u.UpdatedAt,
	}, nil
}

func toFile(f sqlitegen.File) (db.File, error) {
	tenantID, err := toInt32(f.TenantID)
	if err != nil {
		return db.File{}, err
	}
	return db.File{
		ID:             f.ID,
		TenantID:       tenantID,
		Name:           f.Name,
		ContentType:    f.ContentType,
		SizeBytes:      f.SizeBytes,
		ChecksumSha256: f.ChecksumSha256,
		Data:           f.Data,
		CreatedAt:      f.CreatedAt,
	}, nil
}

func toCreateFileRow(r sqlitegen.CreateFileRow) (db.CreateFileRow, error) {
	tenantID, err := toInt32(r.TenantID)
	if err != nil {
		return db.CreateFileRow{}, err
	}
	return db.CreateFileRow{
		ID:             r.ID,
		TenantID:       tenantID,
		Name:           r.Name,
		ContentType:    r.ContentType,
		SizeBytes:      r.SizeBytes,
		ChecksumSha256: r.ChecksumSha256,
		CreatedAt:      r.CreatedAt,
	}, nil
}

func toListAllFilesRow(r sqlitegen.ListAllFilesRow) (db.ListAllFilesRow, error) {
	tenantID, err := toInt32(r.TenantID)
	if err != nil {
		return db.ListAllFilesRow{}, err
	}
	return db.ListAllFilesRow{
		ID:             r.ID,
		TenantID:       tenantID,
		Name:           r.Name,
		ContentType:    r.ContentType,
		SizeBytes:      r.SizeBytes,
		ChecksumSha256: r.ChecksumSha256,
		CreatedAt:      r.CreatedAt,
		TenantName:     r.TenantName,
	}, nil
}

func toForm(f sqlitegen.Form) (db.Form, error) {
	id, err := toInt32(f.ID)
	if err != nil {
		return db.Form{}, err
	}
	tenantID, err := toInt32(f.TenantID)
	if err != nil {
		return db.Form{}, err
	}
	return db.Form{
		ID:              id,
		TenantID:        tenantID,
		Title:           f.Title,
		Slug:            f.Slug,
		Description:     f.Description,
		IsActive:        f.IsActive,
		StartDate:       f.StartDate,
		EndDate:         f.EndDate,
		FormContent:     f.FormContent,
		CreatedAt:       f.CreatedAt,
		UpdatedAt:       f.UpdatedAt,
		PublicAvailable: f.PublicAvailable,
		AcceptAnonymous: f.AcceptAnonymous,
		IsDraft:         f.IsDraft,
	}, nil
}

func toGetAnyFormByIDRow(r sqlitegen.GetAnyFormByIDRow) (db.GetAnyFormByIDRow, error) {
	id, err := toInt32(r.ID)
	if err != nil {
		return db.GetAnyFormByIDRow{}, err
	}
	tenantID, err := toInt32(r.TenantID)
	if err != nil {
		return db.GetAnyFormByIDRow{}, err
	}
	return db.GetAnyFormByIDRow{
		ID:              id,
		TenantID:        tenantID,
		Title:           r.Title,
		Slug:            r.Slug,
		Description:     r.Description,
		IsActive:        r.IsActive,
		StartDate:       r.StartDate,
		EndDate:         r.EndDate,
		FormContent:     r.FormContent,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
		PublicAvailable: r.PublicAvailable,
		AcceptAnonymous: r.AcceptAnonymous,
		IsDraft:         r.IsDraft,
		TenantName:      r.TenantName,
		SubmissionCount: r.SubmissionCount,
	}, nil
}

func toGetFormByIDRow(r sqlitegen.GetFormByIDRow) (db.GetFormByIDRow, error) {
	id, err := toInt32(r.ID)
	if err != nil {
		return db.GetFormByIDRow{}, err
	}
	tenantID, err := toInt32(r.TenantID)
	if err != nil {
		return db.GetFormByIDRow{}, err
	}
	return db.GetFormByIDRow{
		ID:              id,
		TenantID:        tenantID,
		Title:           r.Title,
		Slug:            r.Slug,
		Description:     r.Description,
		IsActive:        r.IsActive,
		StartDate:       r.StartDate,
		EndDate:         r.EndDate,
		FormContent:     r.FormContent,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
		PublicAvailable: r.PublicAvailable,
		AcceptAnonymous: r.AcceptAnonymous,
		IsDraft:         r.IsDraft,
		TenantName:      r.TenantName,
	}, nil
}

func toListAllFormsRow(r sqlitegen.ListAllFormsRow) (db.ListAllFormsRow, error) {
	id, err := toInt32(r.ID)
	if err != nil {
		return db.ListAllFormsRow{}, err
	}
	tenantID, err := toInt32(r.TenantID)
	if err != nil {
		return db.ListAllFormsRow{}, err
	}
	return db.ListAllFormsRow{
		ID:              id,
		TenantID:        tenantID,
		Title:           r.Title,
		Slug:            r.Slug,
		Description:     r.Description,
		IsActive:        r.IsActive,
		StartDate:       r.StartDate,
		EndDate:         r.EndDate,
		FormContent:     r.FormContent,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
		PublicAvailable: r.PublicAvailable,
		AcceptAnonymous: r.AcceptAnonymous,
		IsDraft:         r.IsDraft,
		TenantName:      r.TenantName,
		SubmissionCount: r.SubmissionCount,
	}, nil
}

func toListFormsByTenantRow(r sqlitegen.ListFormsByTenantRow) (db.ListFormsByTenantRow, error) {
	id, err := toInt32(r.ID)
	if err != nil {
		return db.ListFormsByTenantRow{}, err
	}
	tenantID, err := toInt32(r.TenantID)
	if err != nil {
		return db.ListFormsByTenantRow{}, err
	}
	return db.ListFormsByTenantRow{
		ID:              id,
		TenantID:        tenantID,
		Title:           r.Title,
		Slug:            r.Slug,
		Description:     r.Description,
		IsActive:        r.IsActive,
		StartDate:       r.StartDate,
		EndDate:         r.EndDate,
		FormContent:     r.FormContent,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
		PublicAvailable: r.PublicAvailable,
		AcceptAnonymous: r.AcceptAnonymous,
		IsDraft:         r.IsDraft,
		SubmissionCount: r.SubmissionCount,
	}, nil
}

func toFormSubmission(s sqlitegen.FormSubmission) (db.FormSubmission, error) {
	id, err := toInt32(s.ID)
	if err != nil {
		return db.FormSubmission{}, err
	}
	formID, err := toInt32(s.FormID)
	if err != nil {
		return db.FormSubmission{}, err
	}
	userID, err := toInt32Ptr(s.UserID)
	if err != nil {
		return db.FormSubmission{}, err
	}
	return db.FormSubmission{
		ID:          id,
		FormID:      formID,
		Payload:     s.Payload,
		SubmittedAt: s.SubmittedAt,
		UserID:      userID,
	}, nil
}

func toFormWebhook(w sqlitegen.FormWebhook) (db.FormWebhook, error) {
	id, err := toInt32(w.ID)
	if err != nil {
		return db.FormWebhook{}, err
	}
	formID, err := toInt32(w.FormID)
	if err != nil {
		return db.FormWebhook{}, err
	}
	return db.FormWebhook{
		ID:          id,
		FormID:      formID,
		TargetUrl:   w.TargetUrl,
		SecretToken: w.SecretToken,
		IsActive:    w.IsActive,
		CreatedAt:   w.CreatedAt,
	}, nil
}

func toSubmissionMetadatum(m sqlitegen.SubmissionMetadatum) (db.SubmissionMetadatum, error) {
	id, err := toInt32(m.ID)
	if err != nil {
		return db.SubmissionMetadatum{}, err
	}
	submissionID, err := toInt32(m.SubmissionID)
	if err != nil {
		return db.SubmissionMetadatum{}, err
	}
	completionTimeSeconds, err := toInt32Ptr(m.CompletionTimeSeconds)
	if err != nil {
		return db.SubmissionMetadatum{}, err
	}
	return db.SubmissionMetadatum{
		ID:                    id,
		SubmissionID:          submissionID,
		IpAddress:             m.IpAddress,
		UserAgent:             m.UserAgent,
		CompletionTimeSeconds: completionTimeSeconds,
		Referer:               m.Referer,
		CreatedAt:             m.CreatedAt,
	}, nil
}

func toListAllSubmissionsByFormRow(r sqlitegen.ListAllSubmissionsByFormRow) (db.ListAllSubmissionsByFormRow, error) {
	id, err := toInt32(r.ID)
	if err != nil {
		return db.ListAllSubmissionsByFormRow{}, err
	}
	formID, err := toInt32(r.FormID)
	if err != nil {
		return db.ListAllSubmissionsByFormRow{}, err
	}
	userID, err := toInt32Ptr(r.UserID)
	if err != nil {
		return db.ListAllSubmissionsByFormRow{}, err
	}
	completionTimeSeconds, err := toInt32Ptr(r.CompletionTimeSeconds)
	if err != nil {
		return db.ListAllSubmissionsByFormRow{}, err
	}
	return db.ListAllSubmissionsByFormRow{
		ID:                    id,
		FormID:                formID,
		Payload:               r.Payload,
		SubmittedAt:           r.SubmittedAt,
		UserID:                userID,
		IpAddress:             r.IpAddress,
		UserAgent:             r.UserAgent,
		CompletionTimeSeconds: completionTimeSeconds,
		Referer:               r.Referer,
	}, nil
}

func toListSubmissionsByFormRow(r sqlitegen.ListSubmissionsByFormRow) (db.ListSubmissionsByFormRow, error) {
	id, err := toInt32(r.ID)
	if err != nil {
		return db.ListSubmissionsByFormRow{}, err
	}
	formID, err := toInt32(r.FormID)
	if err != nil {
		return db.ListSubmissionsByFormRow{}, err
	}
	userID, err := toInt32Ptr(r.UserID)
	if err != nil {
		return db.ListSubmissionsByFormRow{}, err
	}
	completionTimeSeconds, err := toInt32Ptr(r.CompletionTimeSeconds)
	if err != nil {
		return db.ListSubmissionsByFormRow{}, err
	}
	return db.ListSubmissionsByFormRow{
		ID:                    id,
		FormID:                formID,
		Payload:               r.Payload,
		SubmittedAt:           r.SubmittedAt,
		UserID:                userID,
		IpAddress:             r.IpAddress,
		UserAgent:             r.UserAgent,
		CompletionTimeSeconds: completionTimeSeconds,
		Referer:               r.Referer,
	}, nil
}

// Tenants.

func (s *sqliteQuerier) CreateTenant(ctx context.Context, arg db.CreateTenantParams) (db.Tenant, error) {
	t, err := s.q.CreateTenant(ctx, sqlitegen.CreateTenantParams{
		Name:   arg.Name,
		ApiKey: arg.ApiKey,
	})
	if err != nil {
		return db.Tenant{}, mapSQLiteErr(err)
	}
	return toTenant(t)
}

func (s *sqliteQuerier) GetTenantByAPIKey(ctx context.Context, apiKey string) (db.Tenant, error) {
	t, err := s.q.GetTenantByAPIKey(ctx, apiKey)
	if err != nil {
		return db.Tenant{}, mapSQLiteErr(err)
	}
	return toTenant(t)
}

// Users.

func (s *sqliteQuerier) CreateUser(ctx context.Context, arg db.CreateUserParams) (db.User, error) {
	u, err := s.q.CreateUser(ctx, sqlitegen.CreateUserParams{
		Username:     arg.Username,
		PasswordHash: arg.PasswordHash,
		Role:         string(arg.Role),
	})
	if err != nil {
		return db.User{}, mapSQLiteErr(err)
	}
	return toUser(u)
}

func (s *sqliteQuerier) CreateUserIfNotExists(ctx context.Context, arg db.CreateUserIfNotExistsParams) (int64, error) {
	n, err := s.q.CreateUserIfNotExists(ctx, sqlitegen.CreateUserIfNotExistsParams{
		Username:     arg.Username,
		PasswordHash: arg.PasswordHash,
		Role:         string(arg.Role),
	})
	return n, mapSQLiteErr(err)
}

func (s *sqliteQuerier) GetUserByID(ctx context.Context, id int32) (db.User, error) {
	u, err := s.q.GetUserByID(ctx, int64(id))
	if err != nil {
		return db.User{}, mapSQLiteErr(err)
	}
	return toUser(u)
}

func (s *sqliteQuerier) GetUserByUsername(ctx context.Context, username string) (db.User, error) {
	u, err := s.q.GetUserByUsername(ctx, username)
	if err != nil {
		return db.User{}, mapSQLiteErr(err)
	}
	return toUser(u)
}

func (s *sqliteQuerier) ListUsers(ctx context.Context, arg db.ListUsersParams) ([]db.User, error) {
	rows, err := s.q.ListUsers(ctx, sqlitegen.ListUsersParams{
		PageOffset: int64(arg.PageOffset),
		PageLimit:  int64(arg.PageLimit),
	})
	if err != nil {
		return nil, mapSQLiteErr(err)
	}
	users := make([]db.User, 0, len(rows))
	for _, r := range rows {
		u, err := toUser(r)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

func (s *sqliteQuerier) UpdateUserRole(ctx context.Context, arg db.UpdateUserRoleParams) (db.User, error) {
	u, err := s.q.UpdateUserRole(ctx, sqlitegen.UpdateUserRoleParams{
		ID:   int64(arg.ID),
		Role: string(arg.Role),
	})
	if err != nil {
		return db.User{}, mapSQLiteErr(err)
	}
	return toUser(u)
}

// Files.

func (s *sqliteQuerier) CountAllFiles(ctx context.Context) (int64, error) {
	n, err := s.q.CountAllFiles(ctx)
	return n, mapSQLiteErr(err)
}

func (s *sqliteQuerier) CreateFile(ctx context.Context, arg db.CreateFileParams) (db.CreateFileRow, error) {
	r, err := s.q.CreateFile(ctx, sqlitegen.CreateFileParams{
		ID:             arg.ID,
		TenantID:       int64(arg.TenantID),
		Name:           arg.Name,
		ContentType:    arg.ContentType,
		SizeBytes:      arg.SizeBytes,
		ChecksumSha256: arg.ChecksumSha256,
		Data:           arg.Data,
	})
	if err != nil {
		return db.CreateFileRow{}, mapSQLiteErr(err)
	}
	return toCreateFileRow(r)
}

func (s *sqliteQuerier) DeleteFile(ctx context.Context, arg db.DeleteFileParams) (int64, error) {
	n, err := s.q.DeleteFile(ctx, sqlitegen.DeleteFileParams{
		ID:       arg.ID,
		TenantID: int64(arg.TenantID),
	})
	return n, mapSQLiteErr(err)
}

func (s *sqliteQuerier) GetFileByID(ctx context.Context, id string) (db.File, error) {
	f, err := s.q.GetFileByID(ctx, id)
	if err != nil {
		return db.File{}, mapSQLiteErr(err)
	}
	return toFile(f)
}

func (s *sqliteQuerier) ListAllFiles(ctx context.Context, arg db.ListAllFilesParams) ([]db.ListAllFilesRow, error) {
	rows, err := s.q.ListAllFiles(ctx, sqlitegen.ListAllFilesParams{
		PageOffset: int64(arg.PageOffset),
		PageLimit:  int64(arg.PageLimit),
	})
	if err != nil {
		return nil, mapSQLiteErr(err)
	}
	items := make([]db.ListAllFilesRow, 0, len(rows))
	for _, r := range rows {
		row, err := toListAllFilesRow(r)
		if err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, nil
}

// Forms.

func (s *sqliteQuerier) CountAllForms(ctx context.Context) (int64, error) {
	n, err := s.q.CountAllForms(ctx)
	return n, mapSQLiteErr(err)
}

func (s *sqliteQuerier) CountFormsByTenant(ctx context.Context, arg db.CountFormsByTenantParams) (int64, error) {
	n, err := s.q.CountFormsByTenant(ctx, sqlitegen.CountFormsByTenantParams{
		TenantID: int64(arg.TenantID),
		IsActive: arg.IsActive,
		IsDraft:  arg.IsDraft,
		Search:   arg.Search,
	})
	return n, mapSQLiteErr(err)
}

func (s *sqliteQuerier) CreateForm(ctx context.Context, arg db.CreateFormParams) (db.Form, error) {
	f, err := s.q.CreateForm(ctx, sqlitegen.CreateFormParams{
		TenantID:        int64(arg.TenantID),
		Title:           arg.Title,
		Slug:            arg.Slug,
		Description:     arg.Description,
		IsActive:        arg.IsActive,
		StartDate:       arg.StartDate,
		EndDate:         arg.EndDate,
		FormContent:     arg.FormContent,
		PublicAvailable: arg.PublicAvailable,
		AcceptAnonymous: arg.AcceptAnonymous,
		IsDraft:         arg.IsDraft,
	})
	if err != nil {
		return db.Form{}, mapSQLiteErr(err)
	}
	return toForm(f)
}

func (s *sqliteQuerier) DeleteForm(ctx context.Context, arg db.DeleteFormParams) (int64, error) {
	n, err := s.q.DeleteForm(ctx, sqlitegen.DeleteFormParams{
		ID:       int64(arg.ID),
		TenantID: int64(arg.TenantID),
	})
	return n, mapSQLiteErr(err)
}

func (s *sqliteQuerier) GetAnyFormByID(ctx context.Context, id int32) (db.GetAnyFormByIDRow, error) {
	r, err := s.q.GetAnyFormByID(ctx, int64(id))
	if err != nil {
		return db.GetAnyFormByIDRow{}, mapSQLiteErr(err)
	}
	return toGetAnyFormByIDRow(r)
}

func (s *sqliteQuerier) GetFormByID(ctx context.Context, arg db.GetFormByIDParams) (db.GetFormByIDRow, error) {
	r, err := s.q.GetFormByID(ctx, sqlitegen.GetFormByIDParams{
		ID:       int64(arg.ID),
		TenantID: int64(arg.TenantID),
	})
	if err != nil {
		return db.GetFormByIDRow{}, mapSQLiteErr(err)
	}
	return toGetFormByIDRow(r)
}

func (s *sqliteQuerier) GetPublicFormBySlug(ctx context.Context, slug string) (db.Form, error) {
	f, err := s.q.GetPublicFormBySlug(ctx, slug)
	if err != nil {
		return db.Form{}, mapSQLiteErr(err)
	}
	return toForm(f)
}

func (s *sqliteQuerier) ListAllForms(ctx context.Context, arg db.ListAllFormsParams) ([]db.ListAllFormsRow, error) {
	rows, err := s.q.ListAllForms(ctx, sqlitegen.ListAllFormsParams{
		PageOffset: int64(arg.PageOffset),
		PageLimit:  int64(arg.PageLimit),
	})
	if err != nil {
		return nil, mapSQLiteErr(err)
	}
	items := make([]db.ListAllFormsRow, 0, len(rows))
	for _, r := range rows {
		row, err := toListAllFormsRow(r)
		if err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, nil
}

func (s *sqliteQuerier) ListFormsByTenant(ctx context.Context, arg db.ListFormsByTenantParams) ([]db.ListFormsByTenantRow, error) {
	rows, err := s.q.ListFormsByTenant(ctx, sqlitegen.ListFormsByTenantParams{
		TenantID:   int64(arg.TenantID),
		IsActive:   arg.IsActive,
		IsDraft:    arg.IsDraft,
		Search:     arg.Search,
		PageOffset: int64(arg.PageOffset),
		PageLimit:  int64(arg.PageLimit),
	})
	if err != nil {
		return nil, mapSQLiteErr(err)
	}
	items := make([]db.ListFormsByTenantRow, 0, len(rows))
	for _, r := range rows {
		row, err := toListFormsByTenantRow(r)
		if err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, nil
}

func (s *sqliteQuerier) UpdateForm(ctx context.Context, arg db.UpdateFormParams) (db.Form, error) {
	f, err := s.q.UpdateForm(ctx, sqlitegen.UpdateFormParams{
		Title:            arg.Title,
		Slug:             arg.Slug,
		Description:      arg.Description,
		IsActive:         arg.IsActive,
		StartDate:        arg.StartDate,
		EndDate:          arg.EndDate,
		FormContent:      arg.FormContent,
		PublicAvailable:  arg.PublicAvailable,
		AcceptAnonymous:  arg.AcceptAnonymous,
		IsDraft:          arg.IsDraft,
		ID:               int64(arg.ID),
		TenantID:         int64(arg.TenantID),
		FormContentCheck: string(arg.FormContent),
		IsDraftCheck:     arg.IsDraft,
	})
	if err != nil {
		return db.Form{}, mapSQLiteErr(err)
	}
	return toForm(f)
}

// Submissions.

func (s *sqliteQuerier) CreateFormSubmission(ctx context.Context, arg db.CreateFormSubmissionParams) (db.FormSubmission, error) {
	fs, err := s.q.CreateFormSubmission(ctx, sqlitegen.CreateFormSubmissionParams{
		FormID:  int64(arg.FormID),
		Payload: arg.Payload,
		UserID:  fromInt32Ptr(arg.UserID),
	})
	if err != nil {
		return db.FormSubmission{}, mapSQLiteErr(err)
	}
	return toFormSubmission(fs)
}

func (s *sqliteQuerier) CreateSubmissionMetadata(ctx context.Context, arg db.CreateSubmissionMetadataParams) (db.SubmissionMetadatum, error) {
	var completionTimeSeconds *int64
	if arg.CompletionTimeSeconds != nil {
		v := int64(*arg.CompletionTimeSeconds)
		completionTimeSeconds = &v
	}
	m, err := s.q.CreateSubmissionMetadata(ctx, sqlitegen.CreateSubmissionMetadataParams{
		SubmissionID:          int64(arg.SubmissionID),
		IpAddress:             arg.IpAddress,
		UserAgent:             arg.UserAgent,
		CompletionTimeSeconds: completionTimeSeconds,
		Referer:               arg.Referer,
	})
	if err != nil {
		return db.SubmissionMetadatum{}, mapSQLiteErr(err)
	}
	return toSubmissionMetadatum(m)
}

func (s *sqliteQuerier) ListAllSubmissionsByForm(ctx context.Context, arg db.ListAllSubmissionsByFormParams) ([]db.ListAllSubmissionsByFormRow, error) {
	rows, err := s.q.ListAllSubmissionsByForm(ctx, sqlitegen.ListAllSubmissionsByFormParams{
		FormID:   int64(arg.FormID),
		TenantID: int64(arg.TenantID),
	})
	if err != nil {
		return nil, mapSQLiteErr(err)
	}
	items := make([]db.ListAllSubmissionsByFormRow, 0, len(rows))
	for _, r := range rows {
		row, err := toListAllSubmissionsByFormRow(r)
		if err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, nil
}

func (s *sqliteQuerier) ListSubmissionsByForm(ctx context.Context, arg db.ListSubmissionsByFormParams) ([]db.ListSubmissionsByFormRow, error) {
	rows, err := s.q.ListSubmissionsByForm(ctx, sqlitegen.ListSubmissionsByFormParams{
		FormID:     int64(arg.FormID),
		TenantID:   int64(arg.TenantID),
		PageOffset: int64(arg.PageOffset),
		PageLimit:  int64(arg.PageLimit),
	})
	if err != nil {
		return nil, mapSQLiteErr(err)
	}
	items := make([]db.ListSubmissionsByFormRow, 0, len(rows))
	for _, r := range rows {
		row, err := toListSubmissionsByFormRow(r)
		if err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, nil
}

// Webhooks.

func (s *sqliteQuerier) CreateFormWebhook(ctx context.Context, arg db.CreateFormWebhookParams) (db.FormWebhook, error) {
	w, err := s.q.CreateFormWebhook(ctx, sqlitegen.CreateFormWebhookParams{
		FormID:      int64(arg.FormID),
		TargetUrl:   arg.TargetUrl,
		SecretToken: arg.SecretToken,
	})
	if err != nil {
		return db.FormWebhook{}, mapSQLiteErr(err)
	}
	return toFormWebhook(w)
}

func (s *sqliteQuerier) ListActiveWebhooksByForm(ctx context.Context, formID int32) ([]db.FormWebhook, error) {
	rows, err := s.q.ListActiveWebhooksByForm(ctx, int64(formID))
	if err != nil {
		return nil, mapSQLiteErr(err)
	}
	items := make([]db.FormWebhook, 0, len(rows))
	for _, r := range rows {
		w, err := toFormWebhook(r)
		if err != nil {
			return nil, err
		}
		items = append(items, w)
	}
	return items, nil
}

var _ db.Querier = (*sqliteQuerier)(nil)
