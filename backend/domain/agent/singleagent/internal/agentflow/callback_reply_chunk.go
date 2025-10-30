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
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	singleagent "github.com/coze-dev/coze-studio/backend/crossdomain/agent/model"
	"github.com/coze-dev/coze-studio/backend/crossdomain/plugin/consts"
	plugin "github.com/coze-dev/coze-studio/backend/crossdomain/plugin/model"
	crossworkflow "github.com/coze-dev/coze-studio/backend/crossdomain/workflow"
	"github.com/coze-dev/coze-studio/backend/domain/agent/singleagent/entity"
	"github.com/coze-dev/coze-studio/backend/pkg/lang/conv"
	"github.com/coze-dev/coze-studio/backend/pkg/logs"
)

func newReplyCallback(_ context.Context, executeID string, returnDirectlyTools map[string]struct{}) (clb callbacks.Handler,
	sr *schema.StreamReader[*entity.AgentEvent], sw *schema.StreamWriter[*entity.AgentEvent],
) {
	sr, sw = schema.Pipe[*entity.AgentEvent](10)

	rcc := &replyChunkCallback{
		sw:                  sw,
		executeID:           executeID,
		returnDirectlyTools: returnDirectlyTools,
	}

	clb = callbacks.NewHandlerBuilder().
		OnStartFn(rcc.OnStart).
		OnEndFn(rcc.OnEnd).
		OnEndWithStreamOutputFn(rcc.OnEndWithStreamOutput).
		OnErrorFn(rcc.OnError).
		Build()

	return clb, sr, sw
}

type replyChunkCallback struct {
	sw                  *schema.StreamWriter[*entity.AgentEvent]
	executeID           string
	returnDirectlyTools map[string]struct{}

	// Track current tools invocation
	toolCallOrder   []string            // order of tool_call_ids as produced by assistant
	toolCallID2Name map[string]string   // tool_call_id -> tool name
}

func (r *replyChunkCallback) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	logs.CtxInfof(ctx, "info-OnError, info=%v, err=%v", conv.DebugJsonToStr(info), err)

	switch info.Component {
	case compose.ComponentOfGraph:
		if interruptInfo, ok := compose.ExtractInterruptInfo(err); ok {
			if info.Name != "" {
				return ctx
			}
			interruptData := convInterruptInfo(ctx, interruptInfo)
			interruptData.InterruptID = r.executeID

			toolMessageEvent := &entity.AgentEvent{
				EventType: singleagent.EventTypeOfToolsMessage,
				ToolsMessage: []*schema.Message{
					{
						Role:       schema.Tool,
						Content:    "directly streaming reply",
						ToolCallID: interruptData.ToolCallID,
					},
				},
			}
			r.sw.Send(toolMessageEvent, nil)

			interruptEvent := &entity.AgentEvent{
				EventType: singleagent.EventTypeOfInterrupt,
				Interrupt: interruptData,
			}
			r.sw.Send(interruptEvent, nil)

		} else {
			logs.CtxErrorf(ctx, "[AgentRunError] | node execute failed, component=%v, name=%v, err=%v",
				info.Component, info.Name, err)
			r.sw.Send(nil, err)
		}

	}

	return ctx
}

func (r *replyChunkCallback) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	logs.CtxInfof(ctx, "info-OnStart, info=%v, input=%v", conv.DebugJsonToStr(info), conv.DebugJsonToStr(input))

	switch info.Component {
	case compose.ComponentOfToolsNode:
		if info.Name != keyOfReActAgentToolsNode {
			return ctx
		}
		msg := convToolsNodeCallbackInput(input)
		if msg != nil && len(msg.ToolCalls) > 0 {
			// capture tool_call ids and names for later backfill
			r.toolCallOrder = r.toolCallOrder[:0]
			if r.toolCallID2Name == nil {
				r.toolCallID2Name = make(map[string]string)
			} else {
				for k := range r.toolCallID2Name {
					delete(r.toolCallID2Name, k)
				}
			}
			for _, tc := range msg.ToolCalls {
				id := tc.ID
				name := tc.Function.Name
				if id != "" {
					r.toolCallOrder = append(r.toolCallOrder, id)
					r.toolCallID2Name[id] = name
				}
			}
		}
		ae := &entity.AgentEvent{
			EventType: singleagent.EventTypeOfFuncCall,
			FuncCall:  msg,
		}
		r.sw.Send(ae, nil)
	}

	return ctx
}

func (r *replyChunkCallback) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	logs.CtxInfof(ctx, "info-OnEnd, info=%v, output=%v", conv.DebugJsonToStr(info), conv.DebugJsonToStr(output))
	switch info.Name {
	case keyOfKnowledgeRetriever:
		knowledgeEvent := &entity.AgentEvent{
			EventType: singleagent.EventTypeOfKnowledge,
			Knowledge: retriever.ConvCallbackOutput(output).Docs,
		}

		if knowledgeEvent.Knowledge != nil {
			r.sw.Send(knowledgeEvent, nil)
		}
	case keyOfToolsPreRetriever:
		result := convToolsPreRetrieverCallbackInput(output)

		if len(result) > 0 {
			for _, item := range result {
				var event *entity.AgentEvent
				if item.Role == schema.Tool {
					event = &entity.AgentEvent{
						EventType:    singleagent.EventTypeOfToolsMessage,
						ToolsMessage: []*schema.Message{item},
					}
				} else {
					event = &entity.AgentEvent{
						EventType: singleagent.EventTypeOfFuncCall,
						FuncCall:  item,
					}
				}
				r.sw.Send(event, nil)
			}
		}

	case keyOfSuggestParser:
		sg := convSuggestionNodeCallbackOutput(output)

		if len(sg) > 0 {
			for _, item := range sg {
				suggestionEvent := &entity.AgentEvent{
					EventType: singleagent.EventTypeOfSuggest,
					Suggest:   item,
				}
				r.sw.Send(suggestionEvent, nil)
				}
		}
	case keyOfReActAgentToolsNode:
		msgs := convToolsNodeCallbackOutput(output)
		if len(msgs) > 0 {
			r.sw.Send(&entity.AgentEvent{
				EventType:    singleagent.EventTypeOfToolsMessage,
				ToolsMessage: msgs,
			}, nil)
		}
	default:
		return ctx
	}

	return ctx
}

func (r *replyChunkCallback) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo,
	output *schema.StreamReader[callbacks.CallbackOutput],
) context.Context {
	logs.CtxInfof(ctx, "info-OnEndWithStreamOutput, info=%v, output=%v", conv.DebugJsonToStr(info), conv.DebugJsonToStr(output))
	switch info.Component {
	case compose.ComponentOfGraph, components.ComponentOfChatModel:
		if info.Name != keyOfReActAgentChatModel && info.Name != keyOfLLM {
			output.Close()
			return ctx
		}
		sr := schema.StreamReaderWithConvert(output, func(t callbacks.CallbackOutput) (*schema.Message, error) {
			cbOut := model.ConvCallbackOutput(t)
			return cbOut.Message, nil
		})

		r.sw.Send(&entity.AgentEvent{
			EventType:       singleagent.EventTypeOfChatModelAnswer,
			ChatModelAnswer: sr,
		}, nil)
		return ctx
	case compose.ComponentOfToolsNode:
		// Do not consume tool stream here; the graph needs it to feed tool results back to the model.
		return ctx
	default:
		return ctx
	}
}

func convInterruptInfo(ctx context.Context, interruptInfo *compose.InterruptInfo) *singleagent.InterruptInfo {
	var output *compose.InterruptInfo
	output = interruptInfo.SubGraphs[keyOfReActAgent]
	var extra any

	for i := range output.RerunNodesExtra {
		extra = output.RerunNodesExtra[i]
		break
	}
	toolsNodeExtra, ok := extra.(*compose.ToolsInterruptAndRerunExtra)
	logs.CtxInfof(ctx, "toolsNodeExtra=%v, err=%v", toolsNodeExtra, ok)

	var toolCallID string

	wfResumeData := make(map[string]*crossworkflow.ToolInterruptEvent)
	toolResultData := make(map[string]*plugin.ToolInterruptEvent)
	var interruptEventType singleagent.InterruptEventType
	for k, v := range toolsNodeExtra.RerunExtraMap {
		toolCallID = k

		interruptEventType = convInterruptEventType(v)

		if interruptEventType == singleagent.InterruptEventType_OauthPlugin {
			toolResultData[k] = v.(*plugin.ToolInterruptEvent)
		} else {
			wfResumeData[k] = v.(*crossworkflow.ToolInterruptEvent)
		}
		break
	}

	interrupt := &singleagent.InterruptInfo{
		AllToolInterruptData: toolResultData,
		AllWfInterruptData:   wfResumeData,
		ToolCallID:           toolCallID,
		InterruptType:        interruptEventType,
	}
	return interrupt
}

func convInterruptEventType(interruptEvent any) singleagent.InterruptEventType {
	var interruptEventType singleagent.InterruptEventType

	switch t := interruptEvent.(type) {
	case *crossworkflow.ToolInterruptEvent:
		interruptEventType = singleagent.InterruptEventType(int64(t.EventType))
	case *plugin.ToolInterruptEvent:
		if t.Event == consts.InterruptEventTypeOfToolNeedOAuth {
			interruptEventType = singleagent.InterruptEventType_OauthPlugin
		}
	}
	return interruptEventType
}

func (r *replyChunkCallback) concatToolsNodeOutput(ctx context.Context, output *schema.StreamReader[callbacks.CallbackOutput]) ([]*schema.Message, error) {
	// Group by tool_call_id to avoid index-order mismatches across chunks
	chunksByID := make(map[string][]*schema.Message)
	order := make([]string, 0, 4) // preserve first-seen order of tool_call_ids

	var sr *schema.StreamReader[*schema.Message]
	var sw *schema.StreamWriter[*schema.Message]
	defer func() {
		if sw != nil {
			sw.Close()
		}
	}()
	var streamInitialized bool
	returnDirectByID := make(map[string]bool)
	checkedID := make(map[string]bool)

	for {
		cbOut, err := output.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if sw != nil {
				sw.Send(nil, err)
			}
			return nil, err
		}

		msgs := convToolsNodeCallbackOutput(cbOut)
		for mIndex, msg := range msgs {
			if msg == nil {
				continue
			}

			callID := msg.ToolCallID
			if callID == "" {
				// Fallback key (should rarely happen if upstream sets call_id)
				callID = fmt.Sprintf("idx_%d", mIndex)
			}

			if !checkedID[callID] && len(r.returnDirectlyTools) > 0 {
				checkedID[callID] = true
				// Prefer mapping from OnStart to decide return-directly by tool name
				var toolName string
				if r.toolCallID2Name != nil {
					toolName = r.toolCallID2Name[callID]
				}
				if toolName == "" {
					toolName = msg.ToolName
				}
				if _, ok := r.returnDirectlyTools[toolName]; ok {
					returnDirectByID[callID] = true
				}
				order = append(order, callID)
			} else if _, exists := chunksByID[callID]; !exists {
				// New id discovered after first wave
				order = append(order, callID)
			}

			// Stream directly for this tool_call if configured
			if returnDirectByID[callID] {
				if !streamInitialized {
					sr, sw = schema.Pipe[*schema.Message](5)
					r.sw.Send(&entity.AgentEvent{
						EventType:             singleagent.EventTypeOfToolsAsChatModelStream,
						ToolAsChatModelAnswer: sr,
					}, nil)
					streamInitialized = true
				}
				sw.Send(msg, nil)
			}

			chunksByID[callID] = append(chunksByID[callID], msg)
		}
	}

	// Backfill any missing tool_call_ids (especially returnDirectly tools that may not emit chunks)
	if len(r.toolCallOrder) > 0 {
		for _, callID := range r.toolCallOrder {
			if _, ok := chunksByID[callID]; ok {
				continue
			}
			toolName := ""
			if r.toolCallID2Name != nil {
				toolName = r.toolCallID2Name[callID]
			}
			if toolName != "" {
				if _, isDirect := r.returnDirectlyTools[toolName]; isDirect {
					// synthesize minimal tool message to satisfy model requirement
					chunksByID[callID] = []*schema.Message{{
						Role:       schema.Tool,
						Content:    "directly streaming reply",
						ToolCallID: callID,
						ToolName:   toolName,
					}}
					order = append(order, callID)
				}
			}
		}
		// clear after use
		r.toolCallOrder = nil
		r.toolCallID2Name = nil
	}

	toolMessages := make([]*schema.Message, 0, len(chunksByID))
	for _, callID := range order {
		msgs := chunksByID[callID]
		if len(msgs) == 0 {
			continue
		}
		msg, err := schema.ConcatMessages(msgs)
		if err != nil {
			return nil, err
		}
		if msg.ToolCallID == "" {
			msg.ToolCallID = callID
		}
		// Ensure role and name are preserved
		if msg.Role == "" {
			msg.Role = schema.Tool
		}
		if msg.ToolName == "" && r.toolCallID2Name != nil {
			msg.ToolName = r.toolCallID2Name[callID]
		}
		toolMessages = append(toolMessages, msg)
	}

	return toolMessages, nil
}

func convToolsNodeCallbackInput(input callbacks.CallbackInput) *schema.Message {
	switch t := input.(type) {
	case *schema.Message:
		return t
	default:
		return nil
	}
}

func convToolsNodeCallbackOutput(output callbacks.CallbackOutput) []*schema.Message {
	switch t := output.(type) {
	case []*schema.Message:
		return t
	default:
		return nil
	}
}

func convToolsPreRetrieverCallbackInput(output callbacks.CallbackOutput) []*schema.Message {
	switch t := output.(type) {
	case []*schema.Message:
		return t
	default:
		return nil
	}
}

func convSuggestionNodeCallbackOutput(output callbacks.CallbackInput) []*schema.Message {
	var sg []*schema.Message

	switch so := output.(type) {
	case *schema.Message:
		if so.Content != "" {
			var suggestions []string

			err := json.Unmarshal([]byte(so.Content), &suggestions)

			if err == nil && len(suggestions) > 0 {
				for _, suggestion := range suggestions {
					sm := &schema.Message{
						Role:         so.Role,
						Content:      suggestion,
						ResponseMeta: so.ResponseMeta,
					}
					sg = append(sg, sm)
				}
			}
		}
	default:
		return sg
	}

	return sg
}
