package geolite

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/sync/singleflight"

	"magpie/internal/config"
	"magpie/internal/database"
)

const (
	maxMindDownloadURL = "https://download.maxmind.com/app/geoip_download"
	userAgent          = "magpie-geolite-updater/1.0"
)

var (
	updateGroup singleflight.Group
	httpClient  = &http.Client{Timeout: 2 * time.Minute}
)

var (
	// ErrNoAPIKey indicates that the GeoLite API key has not been configured.
	ErrNoAPIKey = errors.New("geolite: api key is not configured")
)

type downloadTarget struct {
	editionID string
	filename  string
}

var downloadTargets = []downloadTarget{
	{editionID: "GeoLite2-ASN", filename: database.GeoLiteASNFileName},
	{editionID: "GeoLite2-Country", filename: database.GeoLiteCountryFileName},
}

// UpdateDatabases downloads the GeoLite datasets using the configured API key.
// It returns true when an update was performed. If the API key is missing the
// call is skipped and ErrNoAPIKey is returned.
func UpdateDatabases(ctx context.Context) (bool, error) {
	result, err, _ := updateGroup.Do("update", func() (interface{}, error) {
		cfg := config.GetConfig()
		apiKey := strings.TrimSpace(cfg.Plugins.GeoLite.APIKey)
		if apiKey == "" {
			return false, ErrNoAPIKey
		}

		if err := database.EnsureGeoLiteDataDir(); err != nil {
			return false, fmt.Errorf("ensure data dir: %w", err)
		}

		for _, target := range downloadTargets {
			if err := downloadEdition(ctx, apiKey, target); err != nil {
				return false, err
			}
		}

		if err := database.ReloadGeoLiteFromDisk(); err != nil {
			return false, fmt.Errorf("reload geolite: %w", err)
		}

		if err := config.MarkGeoLiteUpdated(time.Now().UTC()); err != nil {
			log.Warn("Failed to persist GeoLite updated timestamp", "error", err)
		}

		if err := PublishGeoLiteDatabases(ctx, nil); err != nil {
			log.Warn("Failed to publish GeoLite databases to redis", "error", err)
		}

		return true, nil
	})

	if err != nil {
		return false, err
	}

	updated, _ := result.(bool)
	return updated, nil
}

func downloadEdition(ctx context.Context, apiKey string, target downloadTarget) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildDownloadURL(apiKey, target.editionID), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", target.editionID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("download %s: unexpected status %d: %s", target.editionID, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	gzipReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: open gzip: %w", target.editionID, err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	targetBase := target.filename
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: read tar: %w", target.editionID, err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(header.Name) != targetBase {
			continue
		}

		destPath := database.GeoLiteFilePath(target.filename)
		if err := writeToFile(destPath, tarReader); err != nil {
			return fmt.Errorf("%s: write file: %w", target.editionID, err)
		}
		return nil
	}

	return fmt.Errorf("%s: mmdb file not found in archive", target.editionID)
}

func writeToFile(destPath string, data io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), "geolite-*.mmdb")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	if _, err := io.Copy(tmpFile, data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("copy data: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpFile.Name(), destPath); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}

	return nil
}

func buildDownloadURL(apiKey, edition string) string {
	return fmt.Sprintf("%s?edition_id=%s&license_key=%s&suffix=tar.gz", maxMindDownloadURL, edition, apiKey)
}
