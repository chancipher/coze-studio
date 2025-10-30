#!/usr/bin/env bash
#
# Copyright 2025 coze-dev Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

set -e

# === 可自定义参数 ===
CONFIGMAP_NAME="opencoze-model-config"
SOURCE_DIR="model"

# === 检查目录是否存在 ===
if [ ! -d "$SOURCE_DIR" ]; then
  echo "❌ 目录不存在: $SOURCE_DIR"
  exit 1
fi

# === 收集指定类型文件 ===
files=()
for f in "$SOURCE_DIR"/*.{json,yaml,yml}; do
  if [ -f "$f" ]; then
    files+=("--from-file=$f")
  fi
done

# === 检查是否有文件 ===
if [ ${#files[@]} -eq 0 ]; then
  echo "⚠️  未找到 .json / .yaml 文件于 $SOURCE_DIR"
  exit 0
fi

# === 删除已有的 ConfigMap（如果存在） ===
kubectl delete configmap "$CONFIGMAP_NAME" -n opencoze 2>/dev/null || true

# === 直接创建 ConfigMap ===
echo "🛠️  创建 ConfigMap: $CONFIGMAP_NAME"
kubectl create configmap "$CONFIGMAP_NAME" \
  "${files[@]}" -n opencoze

echo "✅ 已创建 ConfigMap: $CONFIGMAP_NAME (命名空间: opencoze)"
echo "   可使用以下命令挂载到 Pod："
echo "   volumes:\n     - name: model-config\n       configMap:\n         name: $CONFIGMAP_NAME\n       optional: false"
echo "   volumeMounts:\n     - name: model-config\n       mountPath: /app/resources/conf/model\n       readOnly: true"
