#!/usr/bin/env bash
# 生成 EdgeOne 镜像站点目录，并可一键部署到 EdgeOne Pages
#
# 用法:
#   ./scripts/sync_edgeone.sh <artifacts_dir> <version> [out_dir]
#
# 参数:
#   artifacts_dir  CI 下载的构建产物目录（或本地 dist 目录）
#   version        版本号，例如 v0.4.1
#   out_dir        输出目录（默认 eo-site）
#
# 环境变量:
#   EDGEONE_PREFIX         站点内的子路径，默认 cloudinit（即 https://域名/cloudinit/）
#   EDGEONE_PROJECT_NAME   EdgeOne Pages 项目名，默认 cloudinit
#   EDGEONE_API_TOKEN      EdgeOne API Token；设置后自动部署，否则只生成目录
#
# 产物结构:
#   <out_dir>/<prefix>/latest.json
#   <out_dir>/<prefix>/CloudInit-*.exe
#   <out_dir>/<prefix>/CloudInit-tools.json
#   <out_dir>/<prefix>/CloudInit-tool-*.exe|.cmd
set -euo pipefail

ARTIFACTS_DIR="${1:?用法: sync_edgeone.sh <artifacts_dir> <version> [out_dir]}"
VERSION="${2:?缺少版本号，例如 v0.4.1}"
OUT_DIR="${3:-eo-site}"
PREFIX="${EDGEONE_PREFIX-cloudinit}"

if [ ! -d "$ARTIFACTS_DIR" ]; then
  echo "产物目录不存在: $ARTIFACTS_DIR" >&2
  exit 1
fi

case "$OUT_DIR" in
  ""|"/"|"."|"..")
    echo "非法输出目录: $OUT_DIR" >&2
    exit 1
    ;;
esac

TARGET_DIR="$OUT_DIR/${PREFIX%/}"
rm -rf "$OUT_DIR"
mkdir -p "$TARGET_DIR"

copied=0
for file in "$ARTIFACTS_DIR"/CloudInit-*; do
  [ -f "$file" ] || continue
  cp "$file" "$TARGET_DIR/$(basename "$file")"
  copied=$((copied + 1))
done

echo "已复制 ${copied} 个构建产物到 ${TARGET_DIR}"

python3 - "$TARGET_DIR" "$VERSION" <<'PY'
import datetime
import hashlib
import json
import os
import sys

target_dir, version = sys.argv[1], sys.argv[2]

assets = []
for name in sorted(os.listdir(target_dir)):
    path = os.path.join(target_dir, name)
    if not os.path.isfile(path) or name == "latest.json":
        continue

    with open(path, "rb") as handle:
        digest = hashlib.sha256(handle.read()).hexdigest()

    assets.append({
        "name": name,
        "file": name,
        "size": os.path.getsize(path),
        "sha256": digest,
    })

manifest = {
    "version": version,
    "prerelease": False,
    "published_at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "tools_manifest": "CloudInit-tools.json",
    "assets": assets,
}

manifest_path = os.path.join(target_dir, "latest.json")
with open(manifest_path, "w", encoding="utf-8") as handle:
    json.dump(manifest, handle, ensure_ascii=False, indent=2)

print(f"已生成 {manifest_path}（{len(assets)} 个资产，版本 {version}）")
PY

if [ -n "${EDGEONE_API_TOKEN:-}" ]; then
  PROJECT="${EDGEONE_PROJECT_NAME:-cloudinit}"
  echo "部署到 EdgeOne Pages: project=${PROJECT} dir=${OUT_DIR}"
  edgeone pages deploy "$OUT_DIR" -n "$PROJECT" -t "$EDGEONE_API_TOKEN" -e production
else
  echo "未设置 EDGEONE_API_TOKEN，仅生成站点目录: ${OUT_DIR}"
fi
