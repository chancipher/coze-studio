/*
 * Copyright 2025 coze-dev Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package ppocr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/coze-dev/coze-studio/backend/infra/document/ocr"
	"github.com/coze-dev/coze-studio/backend/pkg/errorx"
	"github.com/coze-dev/coze-studio/backend/types/errno"
)

type Config struct {
	Client *http.Client
	URL    string

	// UseHPS enables Triton/HPS request wrapping and response parsing.
	// If nil, it will be auto-detected by URL containing "/v2/models/".
	UseHPS *bool

	// see: https://paddlepaddle.github.io/PaddleX/latest/pipeline_usage/tutorials/ocr_pipelines/OCR.html#3
	UseDocOrientationClassify *bool
	UseDocUnwarping           *bool
	UseTextlineOrientation    *bool
	TextDetLimitSideLen       *int
	TextDetLimitType          *string
	TextDetThresh             *float64
	TextDetBoxThresh          *float64
	TextDetUnclipRatio        *float64
	TextRecScoreThresh        *float64
}

func NewOCR(config *Config) ocr.OCR {
	return &ppocrImpl{config}
}

type ppocrImpl struct {
	config *Config
}

type ppocrResponse struct {
	Result *ppocrInferResult `json:"result"`
}

type ppocrInferResult struct {
	OCRResults []*ppocrInnerResult `json:"ocrResults"`
}

type ppocrInnerResult struct {
	PrunedResult *ppocrPrunedResult `json:"prunedResult"`
}

type ppocrPrunedResult struct {
	RecTexts       []string              `json:"rec_texts"`
	OverallOCRRes  *ppocrOverallOCRRes   `json:"overall_ocr_res"`
}

type ppocrOverallOCRRes struct {
	RecTexts []string `json:"rec_texts"`
}

// hpsResponse matches Triton HTTP JSON response schema used by PaddleX HPS.
type hpsResponse struct {
	Outputs []struct {
		Name string   `json:"name"`
		Data []string `json:"data"`
	} `json:"outputs"`
}

func (o *ppocrImpl) FromBase64(ctx context.Context, b64 string) ([]string, error) {
	return o.makeRequest(o.newRequestBody(b64))
}

func (o *ppocrImpl) FromURL(ctx context.Context, url string) ([]string, error) {
	return o.makeRequest(o.newRequestBody(url))
}

func (o *ppocrImpl) newRequestBody(file string) map[string]interface{} {
	payload := map[string]interface{}{
		"file":      file,
		"fileType":  1,
		"visualize": false,
	}
	if o.config.UseDocOrientationClassify != nil {
		payload["useDocOrientationClassify"] = *o.config.UseDocOrientationClassify
	} else {
		payload["useDocOrientationClassify"] = false
	}
	if o.config.UseDocUnwarping != nil {
		payload["useDocUnwarping"] = *o.config.UseDocUnwarping
	} else {
		payload["useDocUnwarping"] = false
	}
	if o.config.UseTextlineOrientation != nil {
		payload["useTextlineOrientation"] = *o.config.UseTextlineOrientation
	} else {
		payload["useTextlineOrientation"] = false
	}
	if o.config.TextDetLimitSideLen != nil {
		payload["textDetLimitSideLen"] = *o.config.TextDetLimitSideLen
	}
	if o.config.TextDetLimitType != nil {
		payload["textDetLimitType"] = *o.config.TextDetLimitType
	}
	if o.config.TextDetThresh != nil {
		payload["textDetThresh"] = *o.config.TextDetThresh
	}
	if o.config.TextDetBoxThresh != nil {
		payload["textDetBoxThresh"] = *o.config.TextDetBoxThresh
	}
	if o.config.TextDetUnclipRatio != nil {
		payload["textDetUnclipRatio"] = *o.config.TextDetUnclipRatio
	}
	if o.config.TextRecScoreThresh != nil {
		payload["textRecScoreThresh"] = *o.config.TextRecScoreThresh
	}
	return payload
}

func (o *ppocrImpl) makeRequest(reqBody map[string]interface{}) ([]string, error) {
	// Build request body depending on serving mode
	var bodyBytes []byte
	var err error
	if o.isHPS() {
		// Wrap as Triton inference payload per PaddleX HPS docs
		innerJSON, err := json.Marshal(reqBody)
		if err != nil {
			return nil, errorx.WrapByCode(err, errno.ErrKnowledgeNonRetryableCode)
		}
		wrapper := map[string]interface{}{
			"inputs": []map[string]interface{}{
				{
					"name":     "input",
					"shape":    []int{1, 1},
					"datatype": "BYTES",
					"data":     []string{string(innerJSON)},
				},
			},
			"outputs": []map[string]interface{}{
				{"name": "output"},
			},
		}
		bodyBytes, err = json.Marshal(wrapper)
		if err != nil {
			return nil, errorx.WrapByCode(err, errno.ErrKnowledgeNonRetryableCode)
		}
	} else {
		bodyBytes, err = json.Marshal(reqBody)
		if err != nil {
			return nil, errorx.WrapByCode(err, errno.ErrKnowledgeNonRetryableCode)
		}
	}

	req, err := http.NewRequest("POST", o.config.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, errorx.WrapByCode(err, errno.ErrKnowledgeNonRetryableCode)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.config.Client.Do(req)
	if err != nil {
		return nil, errorx.WrapByCode(err, errno.ErrKnowledgeNonRetryableCode)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errorx.WrapByCode(err, errno.ErrKnowledgeNonRetryableCode)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errorx.WrapByCode(fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody)), errno.ErrKnowledgeNonRetryableCode)
	}

	// Try basic serving response first
	var basic ppocrResponse
	if err := json.Unmarshal(respBody, &basic); err == nil {
		if rec, ok := extractRecTexts(basic); ok {
			return rec, nil
		}
	}

	// Fallback to HPS (Triton) response parsing
	var hps hpsResponse
	if err := json.Unmarshal(respBody, &hps); err == nil {
		if len(hps.Outputs) > 0 && len(hps.Outputs[0].Data) > 0 {
			inner := hps.Outputs[0].Data[0]
			var wrapped ppocrResponse
			if err := json.Unmarshal([]byte(inner), &wrapped); err == nil {
				if rec, ok := extractRecTexts(wrapped); ok {
					return rec, nil
				}
			}
		}
	}

	return nil, errorx.WrapByCode(fmt.Errorf("invalid response body: %s", string(respBody)), errno.ErrKnowledgeNonRetryableCode)
}

func (o *ppocrImpl) isHPS() bool {
	if o.config == nil {
		return false
	}
	if o.config.UseHPS != nil {
		return *o.config.UseHPS
	}
	// Auto-detect by URL pattern
	return strings.Contains(o.config.URL, "/v2/models/")
}

func extractRecTexts(res ppocrResponse) ([]string, bool) {
	if res.Result == nil ||
		res.Result.OCRResults == nil ||
		len(res.Result.OCRResults) == 0 ||
		res.Result.OCRResults[0] == nil ||
		res.Result.OCRResults[0].PrunedResult == nil {
		return nil, false
	}
	pr := res.Result.OCRResults[0].PrunedResult
	// Prefer prunedResult.overall_ocr_res.rec_texts when available (HPS variant)
	if pr.OverallOCRRes != nil {
		if pr.OverallOCRRes.RecTexts != nil {
			return pr.OverallOCRRes.RecTexts, true
		}
		// overall_ocr_res present but no rec_texts -> treat as empty result
		return []string{}, true
	}
	// Fallback to prunedResult.rec_texts
	if pr.RecTexts != nil {
		return pr.RecTexts, true
	}
	// pruned result present but no fields -> empty result
	return []string{}, true
}
