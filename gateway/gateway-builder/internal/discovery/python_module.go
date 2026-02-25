/*
 * Copyright (c) 2025, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package discovery

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PythonModuleInfo contains resolved Python module information
type PythonModuleInfo struct {
	Path    string // Full path (e.g., "github.com/wso2/python-policies/my-policy")
	Version string // Version tag (e.g., "v1.0.0")
	Dir     string // Local directory path
}

// FetchPythonModule downloads a Python policy from a GitHub tarball reference.
// Format: "github.com/<owner>/<repo>/<path>@<version>"
//
// Steps:
//   1. Parse the reference into owner, repo, path, version
//   2. Download https://github.com/<owner>/<repo>/archive/refs/tags/<version>.tar.gz
//   3. Extract to a temp directory
//   4. Return the path to the extracted policy directory
func FetchPythonModule(pythonmodule string) (*PythonModuleInfo, error) {
	slog.Info("Fetching Python module", "reference", pythonmodule, "phase", "discovery")

	// Parse the reference
	owner, repo, path, version, err := parsePythonModuleRef(pythonmodule)
	if err != nil {
		return nil, err
	}

	// Create temp directory for extraction
	tempDir, err := os.MkdirTemp("", "python-policy-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Construct download URL
	downloadURL := fmt.Sprintf("https://github.com/%s/%s/archive/refs/tags/%s.tar.gz", owner, repo, version)
	slog.Debug("Downloading tarball", "url", downloadURL, "phase", "discovery")

	// Download tarball
	tarballPath := filepath.Join(tempDir, "download.tar.gz")
	if err := downloadFile(downloadURL, tarballPath); err != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("failed to download tarball: %w", err)
	}

	// Extract tarball
	extractDir := filepath.Join(tempDir, "extracted")
	if err := extractTarGz(tarballPath, extractDir); err != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("failed to extract tarball: %w", err)
	}

	// Find the extracted directory (it will be named like "repo-version")
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("failed to read extraction directory: %w", err)
	}

	if len(entries) != 1 || !entries[0].IsDir() {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("unexpected extraction structure")
	}

	rootExtractedDir := filepath.Join(extractDir, entries[0].Name())

	// Construct final policy path
	policyPath := filepath.Join(rootExtractedDir, path)

	// Verify the policy exists
	if _, err := os.Stat(policyPath); err != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("policy path not found in tarball: %s", path)
	}

	slog.Info("Successfully fetched Python module",
		"reference", pythonmodule,
		"extractedPath", policyPath,
		"phase", "discovery")

	return &PythonModuleInfo{
		Path:    pythonmodule,
		Version: version,
		Dir:     policyPath,
	}, nil
}

// parsePythonModuleRef parses a Python module reference.
// Format: "github.com/<owner>/<repo>/<path>@<version>"
func parsePythonModuleRef(ref string) (owner, repo, path, version string, err error) {
	// Remove github.com/ prefix if present
	ref = strings.TrimPrefix(ref, "github.com/")

	// Split by @ to separate path from version
	parts := strings.Split(ref, "@")
	if len(parts) != 2 {
		return "", "", "", "", fmt.Errorf("invalid pythonmodule format, expected 'github.com/owner/repo/path@version': %s", ref)
	}

	version = parts[1]
	pathParts := strings.Split(parts[0], "/")

	if len(pathParts) < 3 {
		return "", "", "", "", fmt.Errorf("invalid pythonmodule format, need at least owner/repo/path: %s", ref)
	}

	owner = pathParts[0]
	repo = pathParts[1]
	path = strings.Join(pathParts[2:], "/")

	return owner, repo, path, version, nil
}

// downloadFile downloads a URL to a local file.
func downloadFile(url, filepath string) error {
	client := &http.Client{
		Timeout: 60 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// extractTarGz extracts a tar.gz file to the specified directory.
func extractTarGz(gzipPath, destDir string) error {
	file, err := os.Open(gzipPath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			outFile, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
		}
	}

	return nil
}
