#!/bin/sh
set -eu

./scripts/prepare-mdbook.sh
rm -rf book
mdbook build
mdbook build .mdbook/zh-CN --dest-dir book/zh-CN
