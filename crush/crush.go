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

package crush

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/xi2/xz"
)

type Crush struct{}

// CreateTar writes a TAR to the destination io.Writer containing the directories and files in the source folder.
func (c *Crush) CreateTar(destination io.Writer, source string) error {
	t := tar.NewWriter(destination)
	defer t.Close()

	if err := filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(source, path)
		if err != nil {
			return fmt.Errorf("uanble to calculate relative path %s -> %s: %w", source, path, err)
		}
		if info.IsDir() {
			rel = fmt.Sprintf("%s/", rel)
		}

		if rel == "./" {
			return nil
		}

		h, err := tar.FileInfoHeader(info, info.Name())
		if err != nil {
			return fmt.Errorf("unable to create TAR header from %+v: %w", info, err)
		}
		h.Name = rel

		if err := t.WriteHeader(h); err != nil {
			return fmt.Errorf("unable to write header %+v: %w", h, err)
		}

		if info.IsDir() {
			return nil
		}

		in, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("unable to open %s: %w", path, err)
		}
		defer in.Close()

		if _, err := io.Copy(t, in); err != nil {
			return fmt.Errorf("unable to copy %s to %s: %w", path, h.Name, err)
		}

		return nil
	}); err != nil {
		return fmt.Errorf("unable to create tar from %s: %w", source, err)
	}

	return nil
}

// CreateTarGz writes a GZIP'd TAR to the destination io.Writer containing the directories and files in the source
// folder.
func (c *Crush) CreateTarGz(destination io.Writer, source string) error {
	gz := gzip.NewWriter(destination)
	defer gz.Close()

	return c.CreateTar(gz, source)
}

// ExtractTar extracts source TAR file to a destination directory.  An arbitrary number of top-level directory
// components can be stripped from each path.
func (c *Crush) ExtractTar(source io.Reader, destination string, stripComponents int) error {
	t := tar.NewReader(source)

	for {
		f, err := t.Next()
		if err != nil && err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("unable to read TAR file: %w", err)
		}

		target := c.strippedPath(f.Name, destination, stripComponents)
		if target == "" {
			continue
		}

		info := f.FileInfo()
		if info.IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("unable to make directory %s: %w", target, err)
			}
		} else if info.Mode()&os.ModeSymlink != 0 {
			if err := c.writeSymlink(f.Linkname, target); err != nil {
				return err
			}
		} else {
			if err := c.writeFile(t, target, info.Mode()); err != nil {
				return err
			}
		}
	}

	return nil
}

// ExtractTarGz extracts source GZIP'd TAR file to a destination directory.  An arbitrary number of top-level directory
// components can be stripped from each path.
func (c *Crush) ExtractTarGz(source io.Reader, destination string, stripComponents int) error {
	gz, err := gzip.NewReader(source)
	if err != nil {
		return fmt.Errorf("unable to create GZIP reader: %w", err)
	}
	defer gz.Close()

	return c.ExtractTar(gz, destination, stripComponents)
}

// ExtractTarXz extracts source XZ'd TAR file to a destination directory.  An arbitrary number of top-level directory
// components can be stripped from each path.
func (c *Crush) ExtractTarXz(source io.Reader, destination string, stripComponents int) error {
	xz, err := xz.NewReader(source, 0)
	if err != nil {
		return fmt.Errorf("unable to create XZ reader: %w", err)
	}

	return c.ExtractTar(xz, destination, stripComponents)
}

// ExtractZip extracts source ZIP file to a destination directory.  An arbitrary number of top-level directory
// components can be stripped from each path.
func (c *Crush) ExtractZip(source *os.File, destination string, stripComponents int) error {
	stat, err := source.Stat()
	if err != nil {
		return fmt.Errorf("unable to stat %s: %w", source.Name(), err)
	}

	z, err := zip.NewReader(source, stat.Size())
	if err != nil {
		return err
	}

	for _, f := range z.File {
		target := c.strippedPath(f.Name, destination, stripComponents)
		if target == "" {
			continue
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		} else {
			if err := c.writeZipEntry(f, target); err != nil {
				return err
			}
		}
	}

	return nil
}

func (Crush) strippedPath(source string, destination string, stripComponents int) string {
	components := strings.Split(source, string(filepath.Separator))

	if len(components) <= stripComponents {
		return ""
	}

	return filepath.Join(append([]string{destination}, components[stripComponents:]...)...)
}

func (Crush) writeFile(source io.Reader, path string, perm os.FileMode) error {
	file := filepath.Dir(path)
	if err := os.MkdirAll(file, 0755); err != nil {
		return fmt.Errorf("unable to create directory %s: %w", file, err)
	}

	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("unable to open file %s: %w", path, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, source); err != nil {
		return fmt.Errorf("unable to write data to %s: %w", path, err)
	}

	return nil
}

func (c Crush) writeZipEntry(file *zip.File, path string) error {
	in, err := file.Open()
	if err != nil {
		return fmt.Errorf("unable to open %s: %w", file.Name, err)
	}
	defer in.Close()

	return c.writeFile(in, path, file.Mode())
}

func (Crush) writeSymlink(oldName string, newName string) error {
	file := filepath.Dir(newName)
	if err := os.MkdirAll(file, 0755); err != nil {
		return fmt.Errorf("unable to create directory %s: %w", file, err)
	}

	if err := os.Symlink(oldName, newName); err != nil {
		return fmt.Errorf("unable to create '%s' as symlink to '%s': %v", newName, oldName, err)
	}

	return nil
}
