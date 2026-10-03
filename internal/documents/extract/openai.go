package extract

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/KaanEmec/vitamux/internal/httpx"
)

// OpenAI Responses API with the PDF as an input file and strict structured output
// (https://platform.openai.com/docs/api-reference/responses). store=false asks OpenAI not to
// keep the response. openai_compatible is the same call against an admin-set base URL, for
// self-hosted servers that implement the Responses API with file input; its key is optional.

const openAIBaseURL = "https://api.openai.com/v1"

// OpenAIConfig configures the OpenAI or (Compatible) OpenAI-compatible extractor. BaseURL
// includes the version prefix, e.g. https://api.openai.com/v1.
type OpenAIConfig struct {
	Compatible             bool
	APIKey, Model, BaseURL string
	Client                 *httpx.Client
}

type openAI struct{ cfg OpenAIConfig }

// NewOpenAI returns the openai extractor, or openai_compatible with cfg.Compatible.
func NewOpenAI(cfg OpenAIConfig) Extractor {
	if cfg.BaseURL == "" {
		cfg.BaseURL = openAIBaseURL
	}
	return &openAI{cfg}
}

func (o *openAI) ID() string {
	if o.cfg.Compatible {
		return OpenAICompatible
	}
	return OpenAI
}

// External is true also for openai_compatible: the server may run on another machine.
func (*openAI) External() bool  { return true }
func (o *openAI) Model() string { return o.cfg.Model }

func (o *openAI) Extract(ctx context.Context, req Request) (Response, error) {
	id := o.ID()
	schema, err := providerSchema(req.Schema, false)
	if err != nil {
		return Response{}, err
	}
	body := map[string]any{
		"model":        o.cfg.Model,
		"instructions": req.Prompt,
		"input": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "input_file", "filename": "document.pdf",
				"file_data": "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(req.PDF)},
			map[string]any{"type": "input_text", "text": userText},
		}}},
		"text": map[string]any{"format": map[string]any{
			"type": "json_schema", "name": "lab_extraction_v1", "strict": true, "schema": schema,
		}},
		"store": false,
	}
	h := http.Header{}
	if o.cfg.APIKey != "" {
		h.Set("Authorization", "Bearer "+o.cfg.APIKey)
	}
	hdr, raw, err := post(ctx, o.cfg.Client, id, strings.TrimRight(o.cfg.BaseURL, "/")+"/responses", h, body)
	if err != nil {
		return Response{}, err
	}

	var r struct {
		ID                string `json:"id"`
		Status            string `json:"status"`
		Model             string `json:"model"`
		IncompleteDetails struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	out := Response{Raw: raw}
	if err := json.Unmarshal(raw, &r); err != nil {
		return out, errorf(ClassInvalidOutput, "%s: response is not JSON", id)
	}
	out.ModelID, out.RequestID = r.Model, hdr.Get("x-request-id")
	if out.RequestID == "" {
		out.RequestID = r.ID
	}
	out.Usage = Usage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens, TotalTokens: r.Usage.TotalTokens}
	switch r.Status {
	case "completed":
	case "incomplete":
		if r.IncompleteDetails.Reason == "content_filter" {
			return out, errorf(ClassRefused, "%s: content filter", id)
		}
		return out, errorf(ClassInvalidOutput, "%s: output incomplete (%s)", id, token(r.IncompleteDetails.Reason))
	default:
		return out, errorf(ClassRejected, "%s: response status %s", id, token(r.Status))
	}
	var text strings.Builder
	for _, item := range r.Output {
		if item.Type != "message" {
			continue // reasoning items
		}
		for _, c := range item.Content {
			switch c.Type {
			case "output_text":
				text.WriteString(c.Text)
			case "refusal":
				return out, errorf(ClassRefused, "%s: the model refused", id)
			}
		}
	}
	out.Extraction, err = decode(id, text.String())
	return out, err
}
