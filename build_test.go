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

package libpak_test

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/buildpacks/libcnb"
	"github.com/buildpacks/libcnb/mocks"
	. "github.com/onsi/gomega"
	"github.com/paketoio/libpak"
	"github.com/sclevine/spec"
	"github.com/stretchr/testify/mock"
)

func testBuild(t *testing.T, context spec.G, it spec.S) {
	var (
		Expect = NewWithT(t).Expect

		applicationPath   string
		buildpackPath     string
		buildpackPlanPath string
		commandPath       string
		environmentWriter *mocks.EnvironmentWriter
		exitHandler       *mocks.ExitHandler
		layerContributor  *mocks.LayerContributor
		layersPath        string
		platformPath      string
		tomlWriter        *mocks.TOMLWriter

		workingDir string
	)

	it.Before(func() {
		var err error

		applicationPath, err = ioutil.TempDir("", "build-application-path")
		Expect(err).NotTo(HaveOccurred())
		applicationPath, err = filepath.EvalSymlinks(applicationPath)
		Expect(err).NotTo(HaveOccurred())

		buildpackPath, err = ioutil.TempDir("", "build-buildpack-path")
		Expect(err).NotTo(HaveOccurred())

		f, err := ioutil.TempFile("", "build-buildpackplan-path")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.Close()).NotTo(HaveOccurred())
		buildpackPlanPath = f.Name()

		commandPath = filepath.Join(buildpackPath, "bin", "build")

		environmentWriter = &mocks.EnvironmentWriter{}
		environmentWriter.On("Write", mock.Anything, mock.Anything).Return(nil)

		exitHandler = &mocks.ExitHandler{}
		exitHandler.On("Error", mock.Anything)

		layerContributor = &mocks.LayerContributor{}

		layersPath, err = ioutil.TempDir("", "build-layers-path")
		Expect(err).NotTo(HaveOccurred())

		platformPath, err = ioutil.TempDir("", "build-platform-path")
		Expect(err).NotTo(HaveOccurred())

		Expect(os.MkdirAll(filepath.Join(platformPath, "bindings", "alpha", "metadata"), 0755)).To(Succeed())
		Expect(ioutil.WriteFile(
			filepath.Join(platformPath, "bindings", "alpha", "metadata", "test-metadata-key"),
			[]byte("test-metadata-value"),
			0644,
		)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(platformPath, "bindings", "alpha", "secret"), 0755)).To(Succeed())
		Expect(ioutil.WriteFile(
			filepath.Join(platformPath, "bindings", "alpha", "secret", "test-secret-key"),
			[]byte("test-secret-value"),
			0644,
		)).To(Succeed())
		Expect(os.Setenv("CNB_BINDINGS", `
[bravo]
[bravo.metadata]
test-metadata-key = "test-metadata-value"

[bravo.secret]
test-secret-key = "test-secret-value"
`))

		tomlWriter = &mocks.TOMLWriter{}
		tomlWriter.On("Write", mock.Anything, mock.Anything).Return(nil)

		Expect(os.Setenv("CNB_STACK_ID", "test-stack-id")).To(Succeed())

		workingDir, err = os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Chdir(applicationPath)).To(Succeed())
	})

	it.After(func() {
		Expect(os.Chdir(workingDir)).To(Succeed())
		Expect(os.Unsetenv("CNB_BINDINGS")).To(Succeed())
		Expect(os.Unsetenv("CNB_STACK_ID")).To(Succeed())

		Expect(os.RemoveAll(applicationPath)).To(Succeed())
		Expect(os.RemoveAll(buildpackPath)).To(Succeed())
		Expect(os.RemoveAll(buildpackPlanPath)).To(Succeed())
		Expect(os.RemoveAll(layersPath)).To(Succeed())
		Expect(os.RemoveAll(platformPath)).To(Succeed())
	})

	it("adds contents of CNB_BINDINGS to platform", func() {
		var ctx libcnb.BuildContext
		libpak.Build(
			func(context libcnb.BuildContext) (libcnb.BuildResult, error) {
				ctx = context
				return libcnb.BuildResult{}, nil
			},
			libcnb.WithArguments([]string{commandPath, layersPath, platformPath, buildpackPlanPath}),
		)

		Expect(ctx.Platform).To(Equal(libcnb.Platform{
			Bindings: libcnb.Bindings{
				"alpha": libcnb.Binding{
					Metadata: map[string]string{
						"test-metadata-key": "test-metadata-value",
					},
					Secret: map[string]string{
						"test-secret-key": "test-secret-value",
					},
				},
				"bravo": libcnb.Binding{
					Metadata: map[string]string{
						"test-metadata-key": "test-metadata-value",
					},
					Secret: map[string]string{
						"test-secret-key": "test-secret-value",
					},
				},
			},
			Environment: map[string]string{},
			Path:        platformPath,
		}))
	})

	it("handles error from BuildFunc", func() {
		libpak.Build(
			func(context libcnb.BuildContext) (libcnb.BuildResult, error) {
				return libcnb.BuildResult{}, fmt.Errorf("test-error")
			},
			libcnb.WithArguments([]string{commandPath, layersPath, platformPath, buildpackPlanPath}),
			libcnb.WithExitHandler(exitHandler),
		)

		Expect(exitHandler.Calls[0].Arguments.Get(0)).To(MatchError("test-error"))
	})

	it("removes stale layers", func() {
		Expect(ioutil.WriteFile(filepath.Join(layersPath, "alpha.toml"), []byte(""), 0644)).To(Succeed())
		Expect(ioutil.WriteFile(filepath.Join(layersPath, "bravo.toml"), []byte(""), 0644)).To(Succeed())
		Expect(ioutil.WriteFile(filepath.Join(layersPath, "store.toml"), []byte(""), 0644)).To(Succeed())

		libpak.Build(
			func(context libcnb.BuildContext) (libcnb.BuildResult, error) {
				l, err := context.Layers.Layer("alpha")
				Expect(err).NotTo(HaveOccurred())

				layerContributor.On("Contribute", mock.Anything).Return(l, nil)
				layerContributor.On("Name").Return("alpha")

				return libcnb.BuildResult{
					Layers: []libcnb.LayerContributor{layerContributor},
				}, nil
			},
			libcnb.WithArguments([]string{commandPath, layersPath, platformPath, buildpackPlanPath}),
			libcnb.WithTOMLWriter(tomlWriter),
		)

		Expect(filepath.Join(layersPath, "alpha.toml")).NotTo(BeARegularFile())
		Expect(filepath.Join(layersPath, "bravo.toml")).NotTo(BeARegularFile())
		Expect(filepath.Join(layersPath, "store.toml")).To(BeARegularFile())
	})
}