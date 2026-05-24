package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type OpenAICodexImageBackground string

const (
	OpenAICodexImageBackgroundTransparent OpenAICodexImageBackground = "transparent"
	OpenAICodexImageBackgroundOpaque      OpenAICodexImageBackground = "opaque"
	OpenAICodexImageBackgroundAuto        OpenAICodexImageBackground = "auto"
)

type OpenAICodexImageQuality string

const (
	OpenAICodexImageQualityLow    OpenAICodexImageQuality = "low"
	OpenAICodexImageQualityMedium OpenAICodexImageQuality = "medium"
	OpenAICodexImageQualityHigh   OpenAICodexImageQuality = "high"
	OpenAICodexImageQualityAuto   OpenAICodexImageQuality = "auto"
)

type OpenAICodexImageGenerationRequest struct {
	Prompt     string                     `json:"prompt"`
	Background OpenAICodexImageBackground `json:"background,omitempty"`
	Model      string                     `json:"model"`
	N          *uint64                    `json:"n,omitempty"`
	Quality    OpenAICodexImageQuality    `json:"quality,omitempty"`
	Size       string                     `json:"size,omitempty"`
}

type OpenAICodexImageEditRequest struct {
	Images     []OpenAICodexImageURL      `json:"images"`
	Prompt     string                     `json:"prompt"`
	Background OpenAICodexImageBackground `json:"background,omitempty"`
	Model      string                     `json:"model"`
	N          *uint64                    `json:"n,omitempty"`
	Quality    OpenAICodexImageQuality    `json:"quality,omitempty"`
	Size       string                     `json:"size,omitempty"`
}

type OpenAICodexImageURL struct {
	ImageURL string `json:"image_url"`
}

type OpenAICodexImageResponse struct {
	Created    uint64                     `json:"created"`
	Data       []OpenAICodexImageData     `json:"data"`
	Background OpenAICodexImageBackground `json:"background,omitempty"`
	Quality    OpenAICodexImageQuality    `json:"quality,omitempty"`
	Size       string                     `json:"size,omitempty"`
}

type OpenAICodexImageData struct {
	B64JSON string `json:"b64_json"`
}

func (c *OpenAICodexClient) GenerateImage(ctx context.Context, request OpenAICodexImageGenerationRequest, extraHeaders http.Header) (OpenAICodexImageResponse, error) {
	return c.postImageRequest(ctx, "images/generations", request, extraHeaders, "image generation")
}

func (c *OpenAICodexClient) EditImage(ctx context.Context, request OpenAICodexImageEditRequest, extraHeaders http.Header) (OpenAICodexImageResponse, error) {
	return c.postImageRequest(ctx, "images/edits", request, extraHeaders, "image edit")
}

func (c *OpenAICodexClient) postImageRequest(ctx context.Context, path string, request any, extraHeaders http.Header, operation string) (OpenAICodexImageResponse, error) {
	if c == nil {
		return OpenAICodexImageResponse{}, fmt.Errorf("OpenAI Codex provider is not configured")
	}
	tokenSource := c.tokenSource
	if tokenSource == nil {
		tokenSource = NewCodexOAuthTokenSourceWithWorkspaceIDs("", c.httpClient, c.allowedWorkspaceIDs)
	}
	accessToken, err := tokenSource.AccessToken(ctx)
	if err != nil {
		return OpenAICodexImageResponse{}, err
	}
	if err := validateOpenAICodexImageRequest(request, operation); err != nil {
		return OpenAICodexImageResponse{}, err
	}
	body, err := json.Marshal(request)
	if err != nil {
		return OpenAICodexImageResponse{}, fmt.Errorf("failed to encode %s request: %w", operation, err)
	}
	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	newHTTPRequest := func(token string) (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, openAICodexAPIURL(c.baseURL, path), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		copyHTTPHeaders(httpReq.Header, extraHeaders)
		httpReq.Header.Set("content-type", "application/json")
		httpReq.Header.Set("accept", "application/json")
		applyOpenAICodexAuthHeaders(httpReq.Header, token)
		httpReq.Header.Set("originator", "codex_cli_rs")
		httpReq.Header.Set("user-agent", "kernforge/openai-codex")
		return httpReq, nil
	}

	for attempt := 0; ; attempt++ {
		httpReq, err := newHTTPRequest(accessToken)
		if err != nil {
			return OpenAICodexImageResponse{}, err
		}
		resp, err := httpClient.Do(httpReq)
		if err != nil {
			return OpenAICodexImageResponse{}, err
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return OpenAICodexImageResponse{}, readErr
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			if recovery, ok := openAICodexUnauthorizedRecovery(tokenSource); ok {
				refreshedToken, err := refreshOpenAICodexTokenAfterUnauthorized(ctx, recovery)
				if err != nil {
					return OpenAICodexImageResponse{}, err
				}
				accessToken = refreshedToken
				continue
			}
		}
		if resp.StatusCode >= 300 {
			return OpenAICodexImageResponse{}, newProviderHTTPErrorWithHeaders("openai-codex", resp.StatusCode, resp.Status, data, summarizeOpenAIRequestBody(body), resp.Header)
		}
		return decodeOpenAICodexImageResponse(data, operation)
	}
}

func validateOpenAICodexImageRequest(request any, operation string) error {
	switch typed := request.(type) {
	case OpenAICodexImageGenerationRequest:
		return validateOpenAICodexImageOptions(typed.Background, typed.Quality, operation)
	case OpenAICodexImageEditRequest:
		return validateOpenAICodexImageOptions(typed.Background, typed.Quality, operation)
	default:
		return nil
	}
}

func validateOpenAICodexImageOptions(background OpenAICodexImageBackground, quality OpenAICodexImageQuality, operation string) error {
	if background != "" && !validOpenAICodexImageBackground(background) {
		return fmt.Errorf("failed to encode %s request: invalid background %q", operation, background)
	}
	if quality != "" && !validOpenAICodexImageQuality(quality) {
		return fmt.Errorf("failed to encode %s request: invalid quality %q", operation, quality)
	}
	return nil
}

func copyHTTPHeaders(dst http.Header, src http.Header) {
	if dst == nil || src == nil {
		return
	}
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func decodeOpenAICodexImageResponse(data []byte, operation string) (OpenAICodexImageResponse, error) {
	var raw struct {
		Created    *uint64                    `json:"created"`
		Data       []OpenAICodexImageData     `json:"data"`
		Background OpenAICodexImageBackground `json:"background"`
		Quality    OpenAICodexImageQuality    `json:"quality"`
		Size       string                     `json:"size"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return OpenAICodexImageResponse{}, fmt.Errorf("failed to decode %s response: %w", operation, err)
	}
	if raw.Created == nil {
		return OpenAICodexImageResponse{}, fmt.Errorf("failed to decode %s response: missing field `created`", operation)
	}
	if raw.Data == nil {
		return OpenAICodexImageResponse{}, fmt.Errorf("failed to decode %s response: missing field `data`", operation)
	}
	if raw.Background != "" && !validOpenAICodexImageBackground(raw.Background) {
		return OpenAICodexImageResponse{}, fmt.Errorf("failed to decode %s response: invalid background %q", operation, raw.Background)
	}
	if raw.Quality != "" && !validOpenAICodexImageQuality(raw.Quality) {
		return OpenAICodexImageResponse{}, fmt.Errorf("failed to decode %s response: invalid quality %q", operation, raw.Quality)
	}
	return OpenAICodexImageResponse{
		Created:    *raw.Created,
		Data:       raw.Data,
		Background: raw.Background,
		Quality:    raw.Quality,
		Size:       raw.Size,
	}, nil
}

func validOpenAICodexImageBackground(value OpenAICodexImageBackground) bool {
	switch value {
	case OpenAICodexImageBackgroundTransparent,
		OpenAICodexImageBackgroundOpaque,
		OpenAICodexImageBackgroundAuto:
		return true
	default:
		return false
	}
}

func validOpenAICodexImageQuality(value OpenAICodexImageQuality) bool {
	switch value {
	case OpenAICodexImageQualityLow,
		OpenAICodexImageQualityMedium,
		OpenAICodexImageQualityHigh,
		OpenAICodexImageQualityAuto:
		return true
	default:
		return false
	}
}
