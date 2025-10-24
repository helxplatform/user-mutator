#!/bin/bash
set -e

echo "Running post-create setup..."

# Install Claude Code
echo "Installing Claude Code..."
npm install -g @anthropic-ai/claude-code

# Add other setup commands here as needed
# pip install -r requirements.txt
# go mod download

echo "Post-create setup complete!"
