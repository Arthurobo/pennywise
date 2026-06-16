package db

import (
	"archive/zip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	backupDBName    = "pennywise.db"
	backupSecretKey = "secret.key"
)

// BackupTo creates a consistent snapshot of the source database at dstPath
// using SQLite's VACUUM INTO. It opens a fresh connection so it works even
// while the main server is running (WAL mode allows concurrent readers).
// The destination file is a standalone database — no WAL or SHM sidecar files.
func BackupTo(srcPath, dstPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	conn, err := openForBackup(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer conn.Close()

	escaped := strings.ReplaceAll(dstPath, "'", "''")
	_, err = conn.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s'", escaped))
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}

// Archive creates a .zip backup containing the database (via VACUUM INTO)
// and the secret key file. ExtractArchive is the inverse.
//
// The secret may be missing (e.g. the user relies on PENNYWISE_SESSION_SECRET
// env var instead). In that case the archive contains only the database,
// and the restore will warn about the missing secret key.
func Archive(dbPath, secretPath, dstZipPath string) error {
	dir, err := os.MkdirTemp("", "pennywise-backup-*")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	dbDst := filepath.Join(dir, backupDBName)
	if err := BackupTo(dbPath, dbDst); err != nil {
		return err
	}

	srcs := map[string]string{backupDBName: dbDst}

	if _, statErr := os.Stat(secretPath); statErr == nil {
		secretDst := filepath.Join(dir, backupSecretKey)
		if err := copyFile(secretPath, secretDst); err != nil {
			return fmt.Errorf("copy secret key: %w", err)
		}
		srcs[backupSecretKey] = secretDst
	}

	zf, err := os.Create(dstZipPath)
	if err != nil {
		return fmt.Errorf("create zip: %w", err)
	}
	defer zf.Close()

	w := zip.NewWriter(zf)
	for name, path := range srcs {
		f, err := os.Open(path)
		if err != nil {
			w.Close()
			return fmt.Errorf("open %s: %w", name, err)
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			w.Close()
			return fmt.Errorf("stat %s: %w", name, err)
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			f.Close()
			w.Close()
			return fmt.Errorf("zip header %s: %w", name, err)
		}
		hdr.Name = name
		hdr.Method = zip.Deflate
		wr, err := w.CreateHeader(hdr)
		if err != nil {
			f.Close()
			w.Close()
			return fmt.Errorf("create zip entry %s: %w", name, err)
		}
		if _, err := io.Copy(wr, f); err != nil {
			f.Close()
			w.Close()
			return fmt.Errorf("write zip entry %s: %w", name, err)
		}
		f.Close()
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close zip: %w", err)
	}
	return nil
}

// ExtractArchive extracts a .zip backup created by Archive into the provided
// directory (typically the data dir). It validates that the zip contains at
// least pennywise.db and that the database is valid. Returns the list of
// files extracted.
func ExtractArchive(srcZipPath, dstDir string) ([]string, error) {
	r, err := zip.OpenReader(srcZipPath)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	var extracted []string
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		dst := filepath.Join(dstDir, f.Name)
		if err := extractZipFile(f, dst); err != nil {
			return extracted, fmt.Errorf("extract %s: %w", f.Name, err)
		}
		extracted = append(extracted, dst)
	}

	dbFound := false
	for _, p := range extracted {
		if filepath.Base(p) == backupDBName {
			dbFound = true
			break
		}
	}
	if !dbFound {
		return extracted, fmt.Errorf("archive does not contain %s", backupDBName)
	}

	for _, p := range extracted {
		if filepath.Base(p) == backupDBName {
			if err := Validate(p); err != nil {
				return extracted, fmt.Errorf("backed-up database is invalid: %w", err)
			}
		}
	}

	return extracted, nil
}

// Validate checks that path exists and is a valid SQLite database.
func Validate(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}

	conn, err := openForValidate(path)
	if err != nil {
		return fmt.Errorf("not a valid SQLite database: %w", err)
	}
	defer conn.Close()

	var ok int
	if err := conn.QueryRowContext(context.Background(), "SELECT 1").Scan(&ok); err != nil {
		return fmt.Errorf("cannot query database: %w", err)
	}
	return nil
}

func openForBackup(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys=OFF", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return conn, nil
}

func openForValidate(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&mode=ro", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return conn, nil
}

func extractZipFile(f *zip.File, dst string) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, src); err != nil {
		return err
	}
	return out.Close()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := out.ReadFrom(in); err != nil {
		return err
	}
	return out.Close()
}
