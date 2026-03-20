package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

const githubRepo = "ideamans/eternal"

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func upgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade [version]",
		Short: "Upgrade et binary from GitHub Releases",
		Long:  "Download and replace the et binary from GitHub Releases. If no version is specified, the latest release is used.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var targetVersion string
			if len(args) > 0 {
				targetVersion = args[0]
				if !strings.HasPrefix(targetVersion, "v") {
					targetVersion = "v" + targetVersion
				}
			}

			// Fetch release info
			rel, err := fetchRelease(targetVersion)
			if err != nil {
				return err
			}

			releaseVersion := strings.TrimPrefix(rel.TagName, "v")
			fmt.Fprintf(os.Stderr, "Current version: %s\n", version)
			fmt.Fprintf(os.Stderr, "Target version:  %s\n", releaseVersion)

			if version == releaseVersion {
				fmt.Fprintln(os.Stderr, "Already up to date.")
				return nil
			}

			// Find matching asset
			assetName := fmt.Sprintf("eternal_%s_%s_%s.tar.gz", releaseVersion, runtime.GOOS, runtime.GOARCH)
			var downloadURL string
			for _, a := range rel.Assets {
				if a.Name == assetName {
					downloadURL = a.BrowserDownloadURL
					break
				}
			}
			if downloadURL == "" {
				return fmt.Errorf("no release asset found for %s/%s: %s", runtime.GOOS, runtime.GOARCH, assetName)
			}

			fmt.Fprintf(os.Stderr, "Downloading %s...\n", assetName)

			// Download archive
			resp, err := http.Get(downloadURL)
			if err != nil {
				return fmt.Errorf("download failed: %w", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
			}

			// Extract "et" binary from tar.gz
			binData, err := extractBinaryFromTarGz(resp.Body, "et")
			if err != nil {
				return fmt.Errorf("extract failed: %w", err)
			}

			// Replace current binary
			execPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("cannot determine executable path: %w", err)
			}

			// Write to temp file next to the binary, then rename (atomic on same filesystem)
			tmpPath := execPath + ".upgrade-tmp"
			if err := os.WriteFile(tmpPath, binData, 0755); err != nil {
				os.Remove(tmpPath)
				return fmt.Errorf("failed to write new binary: %w", err)
			}

			if err := os.Rename(tmpPath, execPath); err != nil {
				os.Remove(tmpPath)
				return fmt.Errorf("failed to replace binary: %w", err)
			}

			fmt.Fprintf(os.Stderr, "Upgraded to %s successfully.\n", releaseVersion)
			return nil
		},
	}
}

func fetchRelease(tagName string) (*ghRelease, error) {
	var url string
	if tagName == "" {
		url = fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)
	} else {
		url = fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", githubRepo, tagName)
	}

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch release info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		if tagName != "" {
			return nil, fmt.Errorf("release %s not found", tagName)
		}
		return nil, fmt.Errorf("no releases found")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API error: HTTP %d", resp.StatusCode)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("failed to parse release info: %w", err)
	}
	return &rel, nil
}

func extractBinaryFromTarGz(r io.Reader, binaryName string) ([]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		// Match the binary name (may be at root or in a subdirectory)
		name := hdr.Name
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		if name == binaryName && hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive", binaryName)
}
