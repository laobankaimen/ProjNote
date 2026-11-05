package main

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"projnote/internal/backup"
	"projnote/internal/platform"
	"projnote/internal/store"
)

func openWithBackup(root string) (*store.Store, error) {
	if _, err := os.Stat(filepath.Join(root, "app.db")); err == nil {
		db, err := sql.Open("sqlite", filepath.Join(root, "app.db"))
		if err != nil {
			return nil, err
		}
		var current int
		err = db.QueryRow("PRAGMA user_version").Scan(&current)
		if err == nil && current > 0 && current < store.SchemaVersion {
			var info backup.Info
			info, err = backup.Create(root, db)
			if err == nil {
				err = backup.Protect(root, info.Name, fmt.Sprintf("schema-upgrade-v%d-to-v%d", current, store.SchemaVersion))
			}
			if err == nil {
				var stage string
				stage, _, err = backup.Extract(filepath.Join(root, "backups", info.Name), root)
				if err == nil {
					err = validateSnapshot(stage)
					_ = os.RemoveAll(stage)
				}
			}
			if err == nil {
				fmt.Println("升级前完整备份：", filepath.Join(root, "backups", info.Name))
			}
		}
		closeErr := db.Close()
		if err != nil {
			return nil, fmt.Errorf("升级前备份失败，未执行迁移: %w", err)
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	return store.Open(root)
}
func relocateOffline(root, target string) error {
	target, err := backup.RelocationTarget(root, target)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(target, 0700); err != nil {
		return err
	}
	lock, err := acquireDestination(target)
	if err != nil {
		return err
	}
	defer lock()
	st, err := openWithBackup(root)
	if err != nil {
		return err
	}
	manager := newBackupManager(root, st)
	info, err := manager.MigrationBackup("data-directory-relocation")
	closeErr := st.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	source := filepath.Join(root, "backups", info.Name)
	stage, manifest, err := backup.Extract(source, target)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = validateSnapshot(stage); err != nil {
		return err
	}
	if err = backup.Install(target, stage); err != nil {
		return err
	}
	// Copy the migration recovery point; historical source backups stay untouched.
	if err = os.MkdirAll(filepath.Join(target, "backups"), 0700); err != nil {
		return err
	}
	if err = copyFile(source, filepath.Join(target, "backups", info.Name)); err != nil {
		return fmt.Errorf("业务数据已复制，但迁移备份复制失败，旧目录完整保留: %w", err)
	}
	if err = backup.Protect(target, info.Name, "data-directory-relocation"); err != nil {
		return err
	}
	dest, err := store.Open(target)
	if err != nil {
		return err
	}
	err = dest.RecordEvent("relocate", "system", info.Name, map[string]any{"source_dir": root, "target_dir": target, "files": len(manifest.Files)})
	closeErr = dest.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Printf("数据迁移完成：%s\n原目录已完整保留：%s\n请用 --data-dir 指定新目录启动。\n", target, root)
	return nil
}
func copyFile(source, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	cerr := out.Close()
	if err == nil {
		err = cerr
	}
	return err
}

func acquireDestination(root string) (func(), error) {
	lock, err := platform.AcquireLock(filepath.Join(root, "app.lock"))
	if err != nil {
		return nil, fmt.Errorf("目标目录已在使用或无法锁定: %w", err)
	}
	return func() { _ = lock.Close() }, nil
}
func newBackupManager(root string, st *store.Store) *backup.Manager {
	return &backup.Manager{Root: root, DB: st.DB, Gate: &st.Mu, Settings: st.Settings, Event: st.RecordEvent, Validate: validateSnapshot}
}
