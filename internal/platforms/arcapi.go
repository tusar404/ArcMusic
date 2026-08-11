/*
 * ● ArcMusic
 * ○ A high-performance engine for streaming music in Telegram voicechats.
 *
 * Copyright (C) 2026 Team Arc
 */

package platforms

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Laky-64/gologging"
	"github.com/amarnathcjd/gogram/telegram"

	"main/internal/config"
	"main/internal/core"
	state "main/internal/core/models"
	"main/internal/utils"
)

const PlatformArcApi state.PlatformName = "ArcApi"

type ArcApiPlatform struct {
	name state.PlatformName
}

func init() {
	Register(80, &ArcApiPlatform{
		name: PlatformArcApi,
	})
}

func (f *ArcApiPlatform) Name() state.PlatformName {
	return f.name
}

func (f *ArcApiPlatform) CanGetTracks(query string) bool {
	return false
}

func (f *ArcApiPlatform) GetTracks(_ string, _ bool) ([]*state.Track, error) {
	return nil, errors.New("arcapi is a download-only platform")
}

func (f *ArcApiPlatform) CanDownload(source state.PlatformName) bool {
	if config.ArcAPIURL == "" || config.ArcAPIKey == "" {
		return false
	}
	return source == PlatformYouTube
}

func (f *ArcApiPlatform) Download(
	ctx context.Context,
	track *state.Track,
	statusMsg *telegram.NewMessage,
) (string, error) {

	if f := findFile(track); f != "" {
		gologging.Debug("ArcApi: Download -> Local Cached File -> " + f)
		return f, nil
	}

	cdn, err := f.v2Download(ctx, track)
	if err != nil {
		gologging.ErrorF("ArcApi: V2 URL fetch failed: %v", err)
		return "", err
	}

	if telegramExtractRegex.MatchString(cdn) {
		path, err := f.downloadFromTelegramLink(ctx, cdn, track, statusMsg)
		if err != nil {
			gologging.ErrorF("ArcApi: Telegram CDN download failed: %v", err)
			return "", err
		}
		gologging.Info(fmt.Sprintf("✅ V2-API Telegram CDN | %s | Video: %t", track.ID, track.Video))
		return path, nil
	}

	gologging.Info(fmt.Sprintf("✅ V2-API Direct Stream | %s | Video: %t", track.ID, track.Video))
	return cdn, nil
}

func (*ArcApiPlatform) CanSearch() bool { return false }

func (*ArcApiPlatform) Search(string, bool) ([]*state.Track, error) {
	return nil, nil
}

func (f *ArcApiPlatform) v2Download(ctx context.Context, track *state.Track) (string, error) {
	apiURL := strings.TrimRight(config.ArcAPIURL, "/")
	apiKey := config.ArcAPIKey

	query := track.ID
	if query == "" {
		query = track.URL
	}

	reqURL := fmt.Sprintf("%s/youtube/v2/download", apiURL)

	var respData map[string]any
	resp, err := rc.R().
		SetContext(ctx).
		SetQueryParams(map[string]string{
			"api_key": apiKey,
			"query":   query,
			"isVideo": strconv.FormatBool(track.Video),
		}).
		SetResult(&respData).
		Get(reqURL)

	if err != nil {
		return "", fmt.Errorf("failed to reach api: %w", err)
	}
	if resp.IsError() {
		return "", fmt.Errorf("api returned error status: %d", resp.StatusCode())
	}

	if cdn := f.extractCandidate(respData); cdn != "" {
		return f.normalizeURL(cdn, apiURL), nil
	}

	jobID := f.extractJobID(respData)
	if jobID == "" {
		return "", errors.New("failed to extract cdn or job_id from api")
	}

	gologging.DebugF("ArcApi: Polling Job ID: %s", jobID)

	dlURL := f.pollJobStatus(ctx, jobID)
	if dlURL == "" {
		return "", errors.New("job polling did not return a cdn url")
	}

	return f.normalizeURL(dlURL, apiURL), nil
}

func (f *ArcApiPlatform) downloadFromTelegramLink(
	ctx context.Context,
	link string,
	track *state.Track,
	statusMsg *telegram.NewMessage,
) (string, error) {
	matches := telegramExtractRegex.FindStringSubmatch(link)
	if len(matches) < 4 {
		return "", fmt.Errorf("invalid telegram cdn link: %s", link)
	}

	username := matches[2]
	messageID, err := strconv.Atoi(matches[3])
	if err != nil {
		return "", fmt.Errorf("invalid telegram cdn link message id: %w", err)
	}

	msg, err := core.Bot.GetMessageByID(username, int32(messageID))
	if err != nil {
		return "", fmt.Errorf("failed to fetch telegram cdn message: %w", err)
	}

	ext := ".mp3"
	if track.Video {
		ext = ".mp4"
	}
	path := getPath(track, ext)

	if fileExists(path) {
		return path, nil
	}

	dOpts := &telegram.DownloadOptions{FileName: path, Ctx: ctx}
	if statusMsg != nil {
		dOpts.ProgressManager = utils.GetProgress(statusMsg)
	}

	return msg.Download(dOpts)
}

func (f *ArcApiPlatform) pollJobStatus(ctx context.Context, jobID string) string {
	apiURL := strings.TrimRight(config.ArcAPIURL, "/")
	apiKey := config.ArcAPIKey

	retries := 15
	sleepDuration := 3 * time.Second

	reqURL := fmt.Sprintf("%s/youtube/jobStatus", apiURL)

	for attempt := 0; attempt < retries; attempt++ {
		var respData map[string]any
		resp, err := rc.R().
			SetContext(ctx).
			SetQueryParams(map[string]string{
				"api_key": apiKey,
				"job_id":  jobID,
			}).
			SetResult(&respData).
			Get(reqURL)

		if err != nil || resp.IsError() {
			time.Sleep(sleepDuration)
			continue
		}

		status, _ := respData["status"].(string)
		if status != "success" {
			time.Sleep(sleepDuration)
			continue
		}

		job, ok := respData["job"].(map[string]any)
		if !ok {
			time.Sleep(sleepDuration)
			continue
		}

		jobStatus, _ := job["status"].(string)
		if jobStatus != "done" {
			time.Sleep(sleepDuration)
			continue
		}

		if result, ok := job["result"].(map[string]any); ok {
			if cdn, ok := result["cdn"].(string); ok && cdn != "" {
				return cdn
			}
		}

		break
	}
	return ""
}

func (f *ArcApiPlatform) extractCandidate(data map[string]any) string {
	if res, ok := data["result"].(map[string]any); ok {
		if v, ok := res["cdn"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	if v, ok := data["cdn"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return ""
}

func (f *ArcApiPlatform) extractJobID(data map[string]any) string {
	if id, ok := data["job_id"].(string); ok {
		return id
	}
	return ""
}

func (f *ArcApiPlatform) normalizeURL(candidate, apiURL string) string {
	if strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") {
		return candidate
	}
	if strings.HasPrefix(candidate, "/") {
		return apiURL + candidate
	}
	return apiURL + "/" + candidate
}
