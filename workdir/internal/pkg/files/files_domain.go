// Package files holds the uploaded file domain: its data types, the
// Repository port used to store files as database blobs and a Service
// implementing it on top of sqlc.
package files

import "time"

// File is an uploaded file owned by a tenant.
type File struct {
	// ID is a random UUID; files are read publicly by ID, so it must not be
	// guessable.
	ID          string
	TenantID    int32
	Name        string
	ContentType string
	Size        int64
	// Checksum is the hex-encoded SHA-256 of Data.
	Checksum  string
	CreatedAt time.Time
	// Data is the file content. It is only loaded by Repository.GetByID.
	Data []byte
}

// CreateFileInput holds the data needed to store a file.
type CreateFileInput struct {
	TenantID    int32
	Name        string
	ContentType string
	Data        []byte
}
