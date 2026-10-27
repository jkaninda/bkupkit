/*
MIT License

Copyright (c) 2026 Jonas Kaninda

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

// Package store opens a go-storage backend from configuration and maps the
// configured remote path to a key prefix.
package store

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/jkaninda/bkupkit/config"
	gostorage "github.com/jkaninda/go-storage"
)

// Location is an opened backend plus the directory (key prefix) backups live in.
type Location struct {
	gostorage.Storage
	Kind string // local, s3, gcs, azure, sftp, ftp
	// Dir is the key prefix ("" = backend root). Keys are Dir + "/" + file.
	Dir string
	// Display is a human-readable location for logs and notifications.
	Display string
}

// Key returns the storage key of file inside the location.
func (l *Location) Key(file string) string {
	if l.Dir == "" {
		return file
	}
	return l.Dir + "/" + file
}

// CleanDir normalises a remote path into a key prefix without leading/trailing slashes.
func CleanDir(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, `\`, "/"))
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("remote path %q must not contain '..'", p)
		}
	}
	return strings.Trim(path.Clean("/"+p), "/"), nil
}

// Open opens the storage configured in v.
func Open(v *config.Values) (*Location, error) {
	kind := strings.ToLower(v.String(config.Storage))
	if kind == "ssh" || kind == "remote" {
		kind = "sftp"
	}
	dir, err := CleanDir(v.String(config.RemotePath))
	if err != nil {
		return nil, err
	}
	cfg := gostorage.Config{
		LocalPath:              v.String(config.StoragePath),
		S3Region:               v.String(config.S3Region),
		S3Bucket:               v.String(config.S3Bucket),
		S3Profile:              v.String(config.S3Profile),
		S3KeyID:                v.String(config.S3AccessKey),
		S3Secret:               v.String(config.S3SecretKey),
		S3SessionToken:         v.String(config.S3SessionToken),
		S3Endpoint:             v.String(config.S3Endpoint),
		S3ForcePath:            v.Bool(config.S3ForcePathStyle),
		S3DisableTLS:           v.Bool(config.S3DisableSSL),
		S3StorageClass:         v.String(config.S3StorageClass),
		S3SSE:                  sse(v.String(config.S3SSE)),
		S3SSEKMSKeyID:          v.String(config.S3SSEKMSKeyID),
		S3PartSize:             v.Size(config.S3PartSize),
		S3Concurrency:          v.Int(config.S3Concurrency),
		GCSBucket:              v.String(config.GCSBucket),
		GCSEndpoint:            v.String(config.GCSEndpoint),
		GCSCredentialsFile:     v.String(config.GCSCredentialsFile),
		GCSCredentialsJSON:     v.String(config.GCSCredentialsJSON),
		AzureAccount:           v.String(config.AzureAccount),
		AzureKey:               v.String(config.AzureKey),
		AzureContainer:         v.String(config.AzureContainer),
		AzureEndpoint:          v.String(config.AzureEndpoint),
		AzureSASToken:          v.String(config.AzureSAS),
		SFTPHost:               v.String(config.SFTPHost),
		SFTPPort:               strconv.Itoa(v.Int(config.SFTPPort)),
		SFTPUsername:           v.String(config.SFTPUser),
		SFTPPassword:           v.String(config.SFTPPassword),
		SFTPIdentityFile:       v.String(config.SFTPIdentityFile),
		SFTPIdentityPassphrase: v.String(config.SFTPIdentityPassphrase),
		SFTPKnownHostsFile:     v.String(config.SFTPKnownHosts),
		SFTPIgnoreKnownHosts:   v.Bool(config.SFTPIgnoreHostKey),
		FTPHost:                v.String(config.FTPHost),
		FTPPort:                strconv.Itoa(v.Int(config.FTPPort)),
		FTPUsername:            v.String(config.FTPUser),
		FTPPassword:            v.String(config.FTPPassword),
		FTPTLS:                 v.Bool(config.FTPTLS),
	}
	loc := &Location{Kind: kind, Dir: dir}
	var req []string
	switch kind {
	case "local":
		loc.Display = path.Join(cfg.LocalPath, dir)
	case "s3":
		req = []string{config.S3Bucket}
		loc.Display = "s3://" + path.Join(cfg.S3Bucket, dir)
	case "gcs":
		req = []string{config.GCSBucket}
		loc.Display = "gs://" + path.Join(cfg.GCSBucket, dir)
	case "azure":
		req = []string{config.AzureAccount, config.AzureContainer}
		loc.Display = "azure://" + path.Join(cfg.AzureContainer, dir)
	case "sftp":
		// The remote path is the SFTP working directory (it may be absolute).
		req = []string{config.SFTPHost, config.SFTPUser}
		cfg.SFTPDirectory = v.String(config.RemotePath)
		loc.Dir = ""
		loc.Display = "sftp://" + cfg.SFTPHost + ":" + cfg.SFTPPort + "/" + strings.TrimPrefix(cfg.SFTPDirectory, "/")
		if cfg.SFTPPassword == "" && cfg.SFTPIdentityFile == "" {
			return nil, fmt.Errorf("sftp storage needs %s or %s", config.SFTPPassword, config.SFTPIdentityFile)
		}
	case "ftp":
		req = []string{config.FTPHost, config.FTPUser, config.FTPPassword}
		cfg.FTPDirectory = v.String(config.RemotePath)
		loc.Dir = ""
		loc.Display = "ftp://" + cfg.FTPHost + ":" + cfg.FTPPort + "/" + strings.TrimPrefix(cfg.FTPDirectory, "/")
	default:
		return nil, fmt.Errorf("unsupported storage %q (use local, s3, gcs, azure, sftp or ftp)", kind)
	}
	if err := v.Require(req...); err != nil {
		return nil, fmt.Errorf("%s storage: %w", kind, err)
	}
	s, err := gostorage.NewStorage(gostorage.StorageType(kind), cfg)
	if errors.Is(err, gostorage.ErrNoKnownHosts) {
		return nil, fmt.Errorf("%w; set %s, or %s=true to skip verification (insecure)", err, config.SFTPKnownHosts, config.SFTPIgnoreHostKey)
	}
	if err != nil {
		return nil, err
	}
	loc.Storage = s
	return loc, nil
}

func sse(s string) string {
	if strings.EqualFold(s, "aes256") {
		return "AES256"
	}
	return s
}
