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

# Quick Start - Deno Cache Setup
# Run this script for first-time setup or when dependencies change

set -e

cat << "EOF"
╔══════════════════════════════════════════════════════════════╗
║         Deno Cache Setup - Quick Start Guide                ║
╔══════════════════════════════════════════════════════════════╝

📦 Current Package: jsr:@langchain/pyodide-sandbox@0.0.4

EOF

cd "$(dirname "$0")"

if [ -d ".deno_cache" ]; then
    echo "⚠️  Existing cache found"
    echo ""
    read -p "Do you want to regenerate the cache? (y/N): " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "✅ Using existing cache"
        echo ""
        echo "To build Docker image, run from project root:"
        echo "  ./build-docker.sh"
        exit 0
    fi
    echo "🗑️  Removing old cache..."
    rm -rf .deno_cache
fi

echo ""
echo "Step 1/3: Preparing Deno cache..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
./prepare-deno-cache.sh

echo ""
echo "Step 2/3: Testing cache..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
export DENO_DIR=./.deno_cache
RESULT=$(deno run -A jsr:@langchain/pyodide-sandbox@0.0.4 -c "print('✓ Cache test OK')" 2>&1)

if echo "$RESULT" | grep -q "Cache test OK"; then
    echo "✅ Cache test passed!"
else
    echo "❌ Cache test failed!"
    echo "$RESULT"
    exit 1
fi

echo ""
echo "Step 3/3: Ready to build"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "✨ Setup complete!"
echo ""
echo "Next steps:"
echo "  1. cd .."
echo "  2. ./build-docker.sh"
echo ""
echo "Or manually:"
echo "  docker build -f backend/Dockerfile -t coze-studio:latest ."
echo ""
