#!/bin/bash
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

# Script to pre-download Deno cache for Docker build
# This reduces build time and makes builds more reproducible

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CACHE_DIR="${SCRIPT_DIR}/.deno_cache"

echo "Preparing Deno cache..."

# Create cache directory
mkdir -p "${CACHE_DIR}"

# Set DENO_DIR to our local cache
export DENO_DIR="${CACHE_DIR}"

# Cache the pyodide-sandbox dependency
PKG_NAME="jsr:@langchain/pyodide-sandbox@0.0.4"
echo "Caching ${PKG_NAME}..."
deno cache "${PKG_NAME}"

# 自动收集所有依赖
INFO_JSON="deno_deps.json"
echo "Collecting all dependencies info..."
deno info --json "${PKG_NAME}" > "$INFO_JSON"

# 提取所有 jsr: 和 https://jsr.io/ 依赖
cat "$INFO_JSON" | grep -oE 'jsr:[^" ]+' | sort | uniq > deps.txt
cat "$INFO_JSON" | grep -oE 'https://jsr.io/[^" ]+' | sort | uniq >> deps.txt

# 缓存所有依赖
echo "Caching all dependencies..."
while read dep; do
  echo "Caching $dep"
  deno cache "$dep"
done < deps.txt

# Run the sandbox once to ensure everything is cached
echo "Running initial sandbox test..."
deno run -A "${PKG_NAME}" -c "print('Hello, World')"

# 清理临时文件
rm -f "$INFO_JSON" deps.txt

echo "Deno cache prepared successfully at ${CACHE_DIR}"
echo "Cache size: $(du -sh "${CACHE_DIR}" | cut -f1)"
