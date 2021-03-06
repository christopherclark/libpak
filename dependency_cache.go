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

package libpak

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"

	"github.com/BurntSushi/toml"
	"github.com/buildpacks/libcnb"
	"github.com/heroku/color"
	"github.com/paketo-buildpacks/libpak/bard"
)

// DependencyCache allows a user to get an artifact either from a buildpack's cache, a previous download, or to download
// directly.
type DependencyCache struct {

	// CachePath is the location where the buildpack has cached its dependencies.
	CachePath string

	// DownloadPath is the location of all downloads during this execution of the build.
	DownloadPath string

	// Logger is the logger used to write to the console.
	Logger bard.Logger

	// UserAgent is the User-Agent string to use with requests.
	UserAgent string
}

// NewDependencyCache creates a new instance setting the default cache path (<BUILDPACK_PATH>/dependencies) and user
// agent (<BUILDPACK_ID>/<BUILDPACK_VERSION>).
func NewDependencyCache(buildpack libcnb.Buildpack) DependencyCache {
	return DependencyCache{
		CachePath:    filepath.Join(buildpack.Path, "dependencies"),
		DownloadPath: os.TempDir(),
		Logger:       bard.NewLogger(os.Stdout),
		UserAgent:    filepath.Join("%s/%s", buildpack.Info.ID, buildpack.Info.Version),
	}
}

// Artifact returns the path to the artifact.  Resolution of that path follows three tiers:
//
// 1. CachePath
// 2. DownloadPath
// 3. Download from URI
//
// If the BuildpackDependency's SHA256 is not set, the download can never be verified to be up to date and will always
// download, skipping all of the caches.
func (d *DependencyCache) Artifact(dependency BuildpackDependency) (*os.File, error) {
	var (
		actual   BuildpackDependency
		artifact string
		file     string
	)

	if dependency.SHA256 == "" {
		d.Logger.Header("%s Dependency has no SHA256. Skipping cache.",
			color.New(color.FgYellow, color.Bold).Sprint("Warning:"))

		d.Logger.Body("%s from %s", color.YellowString("Downloading"), dependency.URI)
		artifact = filepath.Join(d.DownloadPath, filepath.Base(dependency.URI))
		if err := d.download(dependency.URI, artifact); err != nil {
			return nil, fmt.Errorf("unable to download %s: %w", dependency.URI, err)
		}

		return os.Open(artifact)
	}

	file = filepath.Join(d.CachePath, fmt.Sprintf("%s.toml", dependency.SHA256))
	if _, err := toml.DecodeFile(file, &actual); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("unable to decode download metadata %s: %w", file, err)
	}

	if reflect.DeepEqual(dependency, actual) {
		d.Logger.Body("%s cached download from buildpack", color.GreenString("Reusing"))
		return os.Open(filepath.Join(d.CachePath, dependency.SHA256, filepath.Base(dependency.URI)))
	}

	file = filepath.Join(d.DownloadPath, fmt.Sprintf("%s.toml", dependency.SHA256))
	if _, err := toml.DecodeFile(file, &actual); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("unable to decode download metadata %s: %w", file, err)
	}

	if reflect.DeepEqual(dependency, actual) {
		d.Logger.Body("%s previously cached download", color.GreenString("Reusing"))
		return os.Open(filepath.Join(d.DownloadPath, dependency.SHA256, filepath.Base(dependency.URI)))
	}

	d.Logger.Body("%s from %s", color.YellowString("Downloading"), dependency.URI)
	artifact = filepath.Join(d.DownloadPath, dependency.SHA256, filepath.Base(dependency.URI))
	if err := d.download(dependency.URI, artifact); err != nil {
		return nil, fmt.Errorf("unable to download %s: %w", dependency.URI, err)
	}

	d.Logger.Body("Verifying checksum")
	if err := d.verify(artifact, dependency.SHA256); err != nil {
		return nil, err
	}

	file = filepath.Join(d.DownloadPath, fmt.Sprintf("%s.toml", dependency.SHA256))
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		return nil, fmt.Errorf("unable to make directory %s: %w", filepath.Dir(file), err)
	}

	out, err := os.OpenFile(file, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return nil, fmt.Errorf("unable to open file %s: %w", file, err)
	}
	defer out.Close()

	if err := toml.NewEncoder(out).Encode(dependency); err != nil {
		return nil, fmt.Errorf("unable to write metadata %s: %w", file, err)
	}

	return os.Open(artifact)
}

func (d DependencyCache) download(uri string, destination string) error {
	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return fmt.Errorf("unable to create new GET request for %s: %w", uri, err)
	}

	if d.UserAgent != "" {
		req.Header.Set("User-Agent", d.UserAgent)
	}

	t := &http.Transport{Proxy: http.ProxyFromEnvironment}
	t.RegisterProtocol("file", http.NewFileTransport(http.Dir("/")))

	client := http.Client{Transport: t}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("unable to request %s: %w", uri, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("could not download %s: %d", uri, resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return fmt.Errorf("unable to make directory %s: %w", filepath.Dir(destination), err)
	}

	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("unable to open file %s: %w", destination, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("unable to copy from %s to %s: %w", uri, destination, err)
	}

	return nil
}

func (DependencyCache) verify(path string, expected string) error {
	s := sha256.New()

	in, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("unable to verify %s: %w", path, err)
	}
	defer in.Close()

	if _, err := io.Copy(s, in); err != nil {
		return fmt.Errorf("unable to read %s: %w", path, err)
	}

	actual := hex.EncodeToString(s.Sum(nil))

	if expected != actual {
		return fmt.Errorf("sha256 for %s %s does not match expected %s", path, actual, expected)
	}

	return nil
}
