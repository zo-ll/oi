#!/usr/bin/env python3
"""Search HuggingFace for GGUF models and list repo files."""

import argparse
import json
import re
import sys
import urllib.request
import urllib.error

HF_API = "https://huggingface.co/api/models"


def search_models(keyword, mem_gb):
    """Search HF for GGUF text-generation models, filter by estimated memory."""
    query = f"{keyword}+gguf"
    url = (
        f"{HF_API}?search={query}"
        f"&filter=text-generation&sort=downloads&limit=20"
    )
    try:
        req = urllib.request.Request(url, headers={"User-Agent": "oi/1.0"})
        with urllib.request.urlopen(req, timeout=15) as resp:
            data = json.loads(resp.read().decode())
    except (urllib.error.URLError, OSError) as e:
        print(json.dumps({"error": str(e)}))
        sys.exit(1)

    results = []
    for model in data:
        model_id = model.get("modelId", "")
        downloads = model.get("downloads", 0)
        siblings = model.get("siblings", [])

        # Estimate size from the largest gguf file listed in siblings
        gguf_sizes = []
        for s in siblings:
            fname = s.get("rfilename", "")
            if fname.lower().endswith(".gguf"):
                # Size may not be in search results; we estimate from tags
                pass

        # Try to estimate size from model tags or name
        est_gb = _estimate_size_from_id(model_id)

        # Filter: skip if estimated size exceeds available memory (with margin)
        if est_gb and mem_gb and est_gb > mem_gb * 1.1:
            continue

        results.append({
            "repo": model_id,
            "name": model_id.split("/")[-1] if "/" in model_id else model_id,
            "downloads": downloads,
            "est_size_gb": est_gb,
        })

    print(json.dumps(results))


def _estimate_size_from_id(model_id):
    """Rough size estimate based on parameter count in the name."""
    name = model_id.lower()
    # Look for patterns like 7b, 13b, 70b, 1.5b, 0.5b
    m = re.search(r'(\d+\.?\d*)\s*b', name)
    if m:
        params_b = float(m.group(1))
        # Q4 quantized: ~0.5-0.6 bytes per param -> GB
        return round(params_b * 0.55, 1)
    return None


def list_repo_files(repo):
    """List GGUF files in a HF repo with quant and size info."""
    url = f"{HF_API}/{repo}"
    try:
        req = urllib.request.Request(url, headers={"User-Agent": "oi/1.0"})
        with urllib.request.urlopen(req, timeout=15) as resp:
            data = json.loads(resp.read().decode())
    except (urllib.error.URLError, OSError) as e:
        print(json.dumps({"error": str(e)}))
        sys.exit(1)

    siblings = data.get("siblings", [])
    results = []
    for s in siblings:
        fname = s.get("rfilename", "")
        if not fname.lower().endswith(".gguf"):
            continue
        size_bytes = s.get("size", 0)

        # Try to extract quant from filename
        quant = _extract_quant(fname)

        results.append({
            "filename": fname,
            "quant": quant or "unknown",
            "size_bytes": size_bytes,
        })

    # Sort by size ascending
    results.sort(key=lambda x: x["size_bytes"])
    print(json.dumps(results))


def _extract_quant(filename):
    """Extract quantization level from a GGUF filename."""
    name = filename.upper()
    # Common patterns: Q4_K_M, Q5_K_S, Q8_0, IQ4_XS, etc.
    m = re.search(r'(I?Q\d+_[A-Z0-9_]+)', name)
    if m:
        return m.group(1)
    m = re.search(r'(I?Q\d+_\d+)', name)
    if m:
        return m.group(1)
    m = re.search(r'(F16|F32|BF16)', name)
    if m:
        return m.group(1)
    return None


def main():
    parser = argparse.ArgumentParser(description="Search HuggingFace for GGUF models")
    parser.add_argument("--search", help="Search keyword")
    parser.add_argument("--mem", type=float, default=0, help="Available memory in GB")
    parser.add_argument("--files", help="List GGUF files in a repo")
    args = parser.parse_args()

    if args.search:
        search_models(args.search, args.mem)
    elif args.files:
        list_repo_files(args.files)
    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
