package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// recover runs the startup sweep and rebuilds the index:
//   - everything under .faux3/tmp is removed (interrupted puts and deletes);
//   - a key dir without meta.json is removed (crash before commit);
//   - a meta.json whose key hashes to a different dir is logged and skipped.
func (s *FS) recover() error {
	entries, err := os.ReadDir(s.tmp)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(s.tmp, e.Name())); err != nil {
			return err
		}
	}

	root := filepath.Join(s.dir, "buckets")
	bdirs, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, bd := range bdirs {
		if !bd.IsDir() {
			continue
		}
		b, err := s.loadBucket(filepath.Join(root, bd.Name()), bd.Name())
		if err != nil {
			return err
		}
		if b != nil {
			s.buckets[b.info.Name] = b
		}
	}
	return nil
}

func (s *FS) loadBucket(dir, name string) (*bucket, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ".bucket.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("store: skipping bucket dir without .bucket.json", "dir", dir)
			return nil, nil
		}
		return nil, err
	}
	b := &bucket{idx: newIndex()}
	if err := json.Unmarshal(raw, &b.info); err != nil || b.info.Name != name {
		s.log.Warn("store: skipping bucket dir with bad .bucket.json", "dir", dir)
		return nil, nil
	}
	// Buckets created before the region field existed report the store's
	// configured region.
	if b.info.Region == "" {
		b.info.Region = s.region
	}
	objects := filepath.Join(dir, "objects")
	shards, err := os.ReadDir(objects)
	if errors.Is(err, fs.ErrNotExist) {
		return b, os.MkdirAll(objects, 0o755)
	}
	if err != nil {
		return nil, err
	}
	for _, sh := range shards {
		if !sh.IsDir() {
			continue
		}
		shardDir := filepath.Join(objects, sh.Name())
		kdirs, err := os.ReadDir(shardDir)
		if err != nil {
			return nil, err
		}
		for _, kd := range kdirs {
			if err := s.loadKey(b, filepath.Join(shardDir, kd.Name()), kd.Name()); err != nil {
				return nil, err
			}
		}
	}
	return b, nil
}

func (s *FS) loadKey(b *bucket, dir, hash string) error {
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if errors.Is(err, fs.ErrNotExist) {
		s.log.Warn("store: removing key dir without meta.json", "dir", dir)
		return os.RemoveAll(dir)
	}
	if err != nil {
		return err
	}
	var m Meta
	if err := json.Unmarshal(raw, &m); err != nil {
		s.log.Warn("store: skipping unreadable meta.json", "dir", dir, "err", err)
		return nil
	}
	if keyHash(m.Key) != hash {
		s.log.Warn("store: skipping meta.json whose key does not match its dir", "dir", dir)
		return nil
	}
	b.idx.put(&m)
	return nil
}
