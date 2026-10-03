package extract

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/KaanEmec/vitamux/internal/httpx"
)

// Gemini generateContent with the PDF inline and a response JSON schema
// (https://ai.google.dev/api/generate-content). The key goes in the x-goog-api-key header,
// never the URL.

const (
	geminiBaseURL = "https://generativelanguage.googleapis.com"
	// geminiInlineMax keeps the base64 request under Gemini's 20 MB inline limit. Larger PDFs
	// would need the Files API, which keeps a copy at Google for 48 hours; not supported.
	geminiInlineMax = 14 << 20
)

// GeminiConfig configures the Gemini extractor. BaseURL is for tests.
type GeminiConfig struct {
	APIKey, Model, BaseURL string
	Client                 *httpx.Client
}

type gemini struct{ cfg GeminiConfig }

// NewGemini returns the Gemini extractor.
func NewGemini(cfg GeminiConfig) Extractor {
	if cfg.BaseURL == "" {
		cfg.BaseURL = geminiBaseURL
	}
	return &gemini{cfg}
}

func (*gemini) ID() string      { return Gemini }
func (*gemini) External() bool  { return true }
func (g *gemini) Model() string { return g.cfg.Model }

type geminiPart struct {
	Text       string      `json:"text,omitempty"`
	InlineData *geminiBlob `json:"inlineData,omitempty"`
}

type geminiBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

func (g *gemini) Extract(ctx context.Context, req Request) (Response, error) {
	if len(req.PDF) > geminiInlineMax {
		return Response{}, errorf(ClassTooLarge, "gemini: PDF larger than %d MiB is not sent inline", geminiInlineMax>>20)
	}
	schema, err := providerSchema(req.Schema, true)
	if err != nil {
		return Response{}, err
	}
	body := map[string]any{
		"systemInstruction": map[string]any{"parts": []geminiPart{{Text: req.Prompt}}},
		"contents": []any{map[string]any{"role": "user", "parts": []geminiPart{
			{InlineData: &geminiBlob{MimeType: "application/pdf", Data: base64.StdEncoding.EncodeToString(req.PDF)}},
			{Text: userText},
		}}},
		"generationConfig": map[string]any{
			"responseMimeType":   "application/json",
			"responseJsonSchema": schema,
			"temperature":        0,
		},
	}
	h := http.Header{}
	h.Set("x-goog-api-key", g.cfg.APIKey)
	endpoint := strings.TrimRight(g.cfg.BaseURL, "/") + "/v1beta/models/" + url.PathEscape(g.cfg.Model) + ":generateContent"
	_, raw, err := post(ctx, g.cfg.Client, Gemini, endpoint, h, body)
	if err != nil {
		return Response{}, err
	}

	var res struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
		ModelVersion string `json:"modelVersion"`
		ResponseID   string `json:"responseId"`
	}
	out := Response{Raw: raw}
	if err := json.Unmarshal(raw, &res); err != nil {
		return out, errorf(ClassInvalidOutput, "gemini: response is not JSON")
	}
	out.ModelID, out.RequestID = res.ModelVersion, res.ResponseID
	out.Usage = Usage{InputTokens: res.UsageMetadata.PromptTokenCount, OutputTokens: res.UsageMetadata.CandidatesTokenCount,
		TotalTokens: res.UsageMetadata.TotalTokenCount}
	switch {
	case res.PromptFeedback.BlockReason != "":
		return out, errorf(ClassRefused, "gemini: prompt blocked (%s)", token(res.PromptFeedback.BlockReason))
	case len(res.Candidates) == 0:
		return out, errorf(ClassInvalidOutput, "gemini: no candidate")
	}
	c := res.Candidates[0]
	switch c.FinishReason {
	case "STOP":
	case "MAX_TOKENS":
		return out, errorf(ClassInvalidOutput, "gemini: output truncated (MAX_TOKENS)")
	default:
		return out, errorf(ClassRefused, "gemini: finished with %s", token(c.FinishReason))
	}
	var text strings.Builder
	for _, p := range c.Content.Parts {
		if !p.Thought {
			text.WriteString(p.Text)
		}
	}
	out.Extraction, err = decode(Gemini, text.String())
	return out, err
}

// token returns s when it is a safe provider code, else "other".
func token(s string) string {
	if safeToken.MatchString(s) {
		return s
	}
	return "other"
}
