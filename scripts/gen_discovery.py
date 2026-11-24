#!/usr/bin/env python3
"""Generate data/discovery.json from live Hugging Face Hub metadata.

This is a maintenance script, not a runtime dependency. It records real
repository metadata (id, publisher, tags, file names and file sizes) and never
invents model entries. Alocal's interactive catalogue groups one entry per
Hugging Face repository, so quantizations are not counted as separate models.
"""

import concurrent.futures
import json
import os
import re
import sys
import time
import urllib.parse
import urllib.request
from datetime import datetime, timezone

API = "https://huggingface.co/api/models"
OUT = "data/discovery.json"
TARGET = 430

SEARCHES = [
    {"filter": "gguf", "sort": "downloads", "direction": "-1", "limit": "200", "full": "true"},
    {"search": "code coder instruct GGUF", "filter": "gguf", "sort": "downloads", "direction": "-1", "limit": "120", "full": "true"},
    {"search": "reasoning R1 think GGUF", "filter": "gguf", "sort": "downloads", "direction": "-1", "limit": "120", "full": "true"},
    {"search": "math GGUF", "filter": "gguf", "sort": "downloads", "direction": "-1", "limit": "100", "full": "true"},
    {"search": "multilingual GGUF", "filter": "gguf", "sort": "downloads", "direction": "-1", "limit": "100", "full": "true"},
    {"search": "vision multimodal GGUF", "filter": "gguf", "sort": "downloads", "direction": "-1", "limit": "100", "full": "true"},
    {"search": "embedding GGUF", "filter": "gguf", "sort": "downloads", "direction": "-1", "limit": "100", "full": "true"},
    {"search": "whisper speech audio GGUF", "filter": "gguf", "sort": "downloads", "direction": "-1", "limit": "100", "full": "true"},
    {"search": "1B 3B small GGUF", "filter": "gguf", "sort": "downloads", "direction": "-1", "limit": "100", "full": "true"},
]

QUANT_RE = re.compile(
    r"(?:^|[-_.])((?:IQ[2-5]_[A-Z0-9]+|Q[0-9]+(?:_[A-Z0-9]+)*|F16|BF16|F32|FP16|FP32))(?=$|[-_.])",
    re.I,
)
PARAM_RE = re.compile(r"(?<![0-9.])(\d+(?:\.\d+)?)\s*B(?![a-z])", re.I)
ACTIVE_RE = re.compile(r"(\d+(?:\.\d+)?)B\s*[-_]?\s*A\s*(\d+(?:\.\d+)?)B", re.I)
CTX_RE = re.compile(r"(?<![0-9.])(\d+(?:\.\d+)?)\s*([KM])(?:\b|[-_])", re.I)


def get_json(url, attempts=3):
    last = None
    for i in range(attempts):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "alocal-catalog-generator/1.0"})
            with urllib.request.urlopen(req, timeout=25) as r:
                return json.load(r)
        except Exception as e:  # noqa: BLE001 - generation tool, report at the end
            last = e
            time.sleep(0.4 * (i + 1))
    raise last


def quant_of(name):
    m = QUANT_RE.search(name)
    return m.group(1).upper() if m else ""


def params_of(text):
    m = ACTIVE_RE.search(text)
    if m:
        return float(m.group(1)), float(m.group(2))
    vals = [float(x) for x in PARAM_RE.findall(text)]
    vals = [v for v in vals if 0.1 <= v <= 1000]
    return (max(vals), 0.0) if vals else (0.0, 0.0)


def context_of(text):
    vals = []
    for num, unit in CTX_RE.findall(text):
        n = float(num)
        if unit.upper() == "M":
            n *= 1024
        if 1 <= n <= 2048:
            vals.append(int(n * 1024))
    return max(vals) if vals else 0


def display_name(repo):
    name = repo.split("/", 1)[-1]
    name = re.sub(r"-?GGUF$", "", name, flags=re.I)
    name = re.sub(r"[_-]+", " ", name).strip()
    return name or repo


def base_model(tags):
    for tag in tags:
        if tag.startswith("base_model:"):
            return tag.split(":", 1)[1]
    return ""


def languages(card):
    if not isinstance(card, dict):
        return []
    vals = card.get("language") or []
    if isinstance(vals, str):
        vals = [vals]
    return [str(v) for v in vals][:12]


def license_of(card, tags):
    if isinstance(card, dict) and card.get("license"):
        return str(card["license"])
    for tag in tags:
        if tag.startswith("license:"):
            return tag.split(":", 1)[1]
    return ""


def categories(item, card):
    tags = set(t.lower() for t in item.get("tags") or [])
    text = (item.get("id", "") + " " + base_model(item.get("tags") or [])).lower()
    pipeline = str(item.get("pipeline_tag") or "").lower()
    langs = languages(card)
    cats = []
    if pipeline in {"text-to-speech", "automatic-speech-recognition", "audio-to-audio", "text-to-audio", "voice-conversion"} or any(t in tags for t in {"whisper", "tts", "speech", "audio"}):
        cats.append("audio")
    if pipeline in {"sentence-similarity", "feature-extraction"} or any(t in tags for t in {"embedding", "embeddings", "sentence-similarity"}):
        cats.append("embeddings")
    if pipeline in {"image-text-to-text", "image-classification", "object-detection", "visual-question-answering"} or any(t in tags for t in {"vision", "multimodal", "vision-language", "llava"}):
        cats.append("vision")
    if any(t in tags for t in {"code", "coding"}) or re.search(r"coder|code|starcoder|devstral|qwen.*coder", text):
        cats.append("coding")
    if any(t in tags for t in {"reasoning", "r1"}) or re.search(r"reasoning|deepseek-r1|\br1\b|think", text):
        cats.append("reasoning")
    if any(t in tags for t in {"math", "mathematics"}) or re.search(r"math|gsm8k", text):
        cats.append("mathematics")
    if "multilingual" in tags or "polyglot" in tags or len(langs) > 1:
        cats.append("multilingual")
    params, _ = params_of(text)
    if 0 < params <= 4:
        cats.append("lightweight")
    if pipeline in {"text-generation", "conversational", "text2text-generation"} or any(t in tags for t in {"conversational", "instruct", "chat"}):
        cats.append("general-chat")
    if not cats and params > 0:
        cats.append("general-chat")
    return cats


def enrich_tree(item):
    repo = item["id"]
    url = f"{API}/{urllib.parse.quote(repo, safe='/')}/tree/main?recursive=true&expand=false"
    try:
        tree = get_json(url, attempts=2)
    except Exception:  # noqa: BLE001 - keep real metadata without sizes
        tree = []
    files = []
    for node in tree if isinstance(tree, list) else []:
        name = node.get("path", "")
        size = node.get("size", 0)
        if node.get("type") == "file" and name.lower().endswith(".gguf") and isinstance(size, int) and size > 0:
            files.append({"name": name, "quant": quant_of(name), "size_bytes": size})
    files.sort(key=lambda f: (-f["size_bytes"], f["name"]))
    return repo, files


def single_model_repo(files):
    """Reject bundle repos that ship many unrelated models in subfolders."""
    dirs = {os.path.dirname(f["name"]) for f in files if "/" in f["name"]}
    top_level = sum(1 for f in files if "/" not in f["name"])
    return top_level > 0 or len(dirs) <= 2


def main():
    found = {}
    for query in SEARCHES:
        url = API + "?" + urllib.parse.urlencode(query)
        try:
            rows = get_json(url)
        except Exception as e:  # noqa: BLE001
            print(f"warn: search failed {url}: {e}", file=sys.stderr)
            continue
        for row in rows:
            repo = row.get("id", "")
            if not repo or row.get("private") or row.get("gated") or "/" not in repo:
                continue
            siblings = row.get("siblings") or []
            if not any(str(s.get("rfilename", "")).lower().endswith(".gguf") for s in siblings):
                continue
            if repo not in found:
                found[repo] = row
    ranked = sorted(found.values(), key=lambda r: (r.get("downloads") or 0, r.get("likes") or 0), reverse=True)[:TARGET]
    print(f"found {len(found)} repositories; enriching {len(ranked)}", file=sys.stderr)

    trees = {}
    with concurrent.futures.ThreadPoolExecutor(max_workers=10) as pool:
        for repo, files in pool.map(enrich_tree, ranked):
            trees[repo] = files

    models = []
    for row in ranked:
        repo = row["id"]
        if trees.get(repo) and not single_model_repo(trees[repo]):
            continue
        tags = [str(t) for t in (row.get("tags") or [])]
        text = repo + " " + base_model(tags)
        params, active = params_of(text)
        files = trees.get(repo, [])
        quants = []
        for f in files:
            if f["quant"] and f["quant"] not in quants:
                quants.append(f["quant"])
        if not quants:
            for sibling in row.get("siblings") or []:
                q = quant_of(str(sibling.get("rfilename", "")))
                if q and q not in quants:
                    quants.append(q)
        preferred = None
        for want in ("Q4_K_M", "Q5_K_M", "Q8_0", "Q6_K"):
            preferred = next((f for f in files if f["quant"] == want), None)
            if preferred:
                break
        if not preferred:
            preferred = files[0] if files else None
        card = row.get("cardData") or {}
        models.append({
            "id": repo,
            "repo": repo,
            "publisher": row.get("author") or repo.split("/", 1)[0],
            "display_name": display_name(repo),
            "base_model": base_model(tags),
            "architecture": "",
            "params_b": round(params, 3),
            "active_params_b": round(active, 3),
            "file": {"name": preferred["name"], "quant": preferred["quant"], "size_bytes": preferred["size_bytes"]} if preferred else {},
            "quantizations": quants[:40],
            "files": files[:60],
            "max_context": context_of(text),
            "pipeline": row.get("pipeline_tag") or "",
            "tasks": [str(t) for t in tags if not t.startswith(("base_model:", "license:", "region:", "arxiv:"))][:40],
            "categories": categories(row, card),
            "license": license_of(card, tags),
            "languages": languages(card),
            "downloads": int(row.get("downloads") or 0),
            "likes": int(row.get("likes") or 0),
            "created_at": row.get("createdAt") or "",
            "url": "https://huggingface.co/" + repo,
        })

    payload = {
        "version": 1,
        "source": API,
        "generated_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat(),
        "count": len(models),
        "models": models,
    }
    with open(OUT, "w", encoding="utf-8") as f:
        json.dump(payload, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print(f"wrote {OUT}: {len(models)} repositories", file=sys.stderr)


if __name__ == "__main__":
    main()
