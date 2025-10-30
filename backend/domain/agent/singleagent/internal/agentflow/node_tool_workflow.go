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

package agentflow

import (
	"context"

	"github.com/coze-dev/coze-studio/backend/api/model/app/bot_common"
	crossworkflow "github.com/coze-dev/coze-studio/backend/crossdomain/workflow"
	workflowModel "github.com/coze-dev/coze-studio/backend/crossdomain/workflow/model"
	"github.com/coze-dev/coze-studio/backend/domain/workflow"
	"github.com/coze-dev/coze-studio/backend/domain/workflow/entity/vo"
)

type workflowConfig struct {
	wfInfos []*bot_common.WorkflowInfo
}

func newWorkflowTools(ctx context.Context, conf *workflowConfig) ([]workflow.ToolFromWorkflow, map[string]struct{}, error) {
	var policies []*vo.GetPolicy

	for _, info := range conf.wfInfos {
		id := info.GetWorkflowId()
		policies = append(policies, &vo.GetPolicy{
			ID:    id,
			QType: workflowModel.FromLatestVersion,
		})
	}

	// Do NOT mark workflow tools as return-directly; always return tool messages to the model
	toolsReturnDirectly := make(map[string]struct{})

	workflowTools, err := crossworkflow.DefaultSVC().WorkflowAsModelTool(ctx, policies)

	// Previously we added tools with terminate plan UseAnswerContent to toolsReturnDirectly.
	// This leads to OpenAI rejecting when multiple tool_calls appear in one assistant turn.
	// We now rely on ToolMidAnswer streaming for UX, while still returning proper tool messages.

	return workflowTools, toolsReturnDirectly, err
}
