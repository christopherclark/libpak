/*
 * Copyright 2018-2020 the original author or authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      https://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package sherpa

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// FileEntry is metadata about a file.
type FileEntry struct {

	// Path is the path of a file.
	Path string `mapstructure:"path" toml:"path"`

	// Mode is the mode of the source file.
	Mode string `mapstructure:"mode" toml:"mode"`

	// ModificationTime is the modification time of the file.
	ModificationTime string `mapstructure:"modification-time" toml:"modification-time"`

	// SHA256 is the SHA256 has of the source file.
	SHA256 string `mapstructure:",omitempty" toml:"sha256,omitempty"`
}

type result struct {
	err   error
	value FileEntry
}

// NewFileListing generates a listing of all entries under root.
func NewFileListing(root string) ([]FileEntry, error) {
	ch := make(chan result)
	var wg sync.WaitGroup

	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if path == root {
			return nil
		}

		wg.Add(1)
		go func() {
			defer wg.Done()

			e := FileEntry{
				Path:             path,
				Mode:             info.Mode().String(),
				ModificationTime: info.ModTime().Format(time.RFC3339),
			}

			if info.IsDir() {
				ch <- result{value: e}
				return
			}

			s := sha256.New()

			in, err := os.Open(path)
			if err != nil {
				ch <- result{err: fmt.Errorf("unable to open file %s: %w", path, err)}
				return
			}
			defer in.Close()

			if _, err := io.Copy(s, in); err != nil {
				ch <- result{err: fmt.Errorf("unable to hash file %s: %w", path, err)}
				return
			}

			e.SHA256 = hex.EncodeToString(s.Sum(nil))
			ch <- result{value: e}
		}()

		return nil
	}); err != nil {
		return nil, fmt.Errorf("error walking path %s: %w", root, err)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	var e []FileEntry
	for r := range ch {
		if r.err != nil {
			return nil, fmt.Errorf("unable to create file listing: %s", r.err)
		}
		e = append(e, r.value)
	}
	sort.Slice(e, func(i, j int) bool {
		return e[i].Path < e[j].Path
	})

	return e, nil
}
