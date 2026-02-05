#!/usr/bin/env python3
"""Search HuggingFace for GGUF models and list repo files."""

import argparse
import json
import re
import sys
import urllib.request
import urllib.error

HF_API = "https://huggingface.co/api/models"


def search_models(keyword, mem_gb, format_filter=None):
    """Search HF for models with optional format filtering."""
    # Build base URL with search parameters
    params = {
        "search": keyword,
        "sort": "downloads",
        "direction": "-1",
        "limit": "20",
    }

    # Add format filter if specified (use HuggingFace API filter parameter)
    if format_filter:
        params["filter"] = format_filter

    # Build URL with parameters
    url = HF_API + "?" + "&".join(f"{k}={v}" for k, v in params.items())

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
        
        # Extract format from tags
        tags = model.get("tags", [])
        model_format = None
        format_priority = ["gguf", "safetensors", "pytorch", "tensorflow", "onnx", "jax", "coreml", "openvino"]
        for fmt in format_priority:
            if fmt in tags:
                model_format = fmt
                break
        
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
            "format": model_format or "unknown",
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


def _is_shard(filename):
    """Check if file is a shard (part of a split model)."""
    return bool(re.search(r'-\d{5}-of-\d{5}', filename))


def _shard_total(filename):
    """Get total shard count from filename like -00001-of-00004."""
    m = re.search(r'-\d{5}-of-(\d{5})', filename)
    return int(m.group(1)) if m else 1


# Model weight file extensions we care about
_MODEL_EXTENSIONS = {
    ".gguf": "GGUF",
    ".safetensors": "Safetensors",
    ".bin": "PyTorch",
    ".pt": "PyTorch",
    ".pth": "PyTorch",
    ".onnx": "ONNX",
    ".nemo": "NeMo",
    ".ggml": "GGML",
    ".mlpackage": "CoreML",
    ".mlmodelc": "CoreML",
    ".tflite": "TFLite",
    ".h5": "Keras",
    ".keras": "Keras",
    ".mar": "TorchServe",
    ".engine": "TensorRT",
}

# Files to always skip even if extension matches
_SKIP_PATTERNS = [
    "tokenizer", "config", "vocab", "merges", "special_tokens",
    "generation_config", "preprocessor", "trainer_state",
    "training_args", "optimizer", "scheduler", "added_tokens",
]


def _get_format(filename):
    """Get model format from filename, or None if not a model file."""
    lower = filename.lower()
    basename = lower.rsplit("/", 1)[-1]

    # Skip known non-model files
    for skip in _SKIP_PATTERNS:
        if skip in basename:
            return None

    for ext, fmt in _MODEL_EXTENSIONS.items():
        if lower.endswith(ext):
            return fmt
    return None


def _is_shard_generic(filename):
    """Check if file is a shard (any format)."""
    # Patterns: -00001-of-00004, .part1of4, etc.
    return bool(re.search(r'-\d{5}-of-\d{5}', filename))


def _shard_key(filename):
    """Strip shard suffix to get a grouping key."""
    # Remove -NNNNN-of-NNNNN before the extension
    return re.sub(r'-\d{5}-of-\d{5}', '', filename)


def list_repo_files(repo):
    """List model weight files in a HF repo, grouped by quant/shard."""
    url = f"{HF_API}/{repo}"
    try:
        req = urllib.request.Request(url, headers={"User-Agent": "oi/1.0"})
        with urllib.request.urlopen(req, timeout=15) as resp:
            data = json.loads(resp.read().decode())
    except (urllib.error.URLError, OSError) as e:
        print(json.dumps({"error": str(e)}))
        sys.exit(1)

    siblings = data.get("siblings", [])

    # Collect all model weight files
    model_files = []
    for s in siblings:
        fname = s.get("rfilename", "")
        fmt = _get_format(fname)
        if fmt is None:
            continue
        size_bytes = s.get("size", 0)
        quant = _extract_quant(fname)
        model_files.append({
            "filename": fname,
            "format": fmt,
            "quant": quant or "-",
            "size_bytes": size_bytes,
        })

    # Group sharded files
    groups = {}
    singles = []

    for f in model_files:
        fname = f["filename"]
        if _is_shard_generic(fname):
            key = _shard_key(fname)
            if key not in groups:
                groups[key] = {
                    "format": f["format"],
                    "quant": f["quant"],
                    "files": [],
                    "total_size": 0,
                    "shard_count": _shard_total(fname),
                }
            groups[key]["files"].append(fname)
            groups[key]["total_size"] += f["size_bytes"]
        else:
            singles.append(f)

    # Build output
    results = []
    for f in singles:
        results.append({
            "filename": f["filename"],
            "format": f["format"],
            "quant": f["quant"],
            "size_bytes": f["size_bytes"],
            "shard_count": 1,
            "all_files": [f["filename"]],
        })

    for key in sorted(groups.keys()):
        g = groups[key]
        g["files"].sort()
        # Display name: strip shard numbering from first file
        display = re.sub(r'-\d{5}-of-\d{5}', '', g["files"][0])
        results.append({
            "filename": display,
            "format": g["format"],
            "quant": g["quant"],
            "size_bytes": g["total_size"],
            "shard_count": g["shard_count"],
            "all_files": g["files"],
        })

    # Sort by size ascending
    results.sort(key=lambda x: x["size_bytes"])
    print(json.dumps(results))


def main():
    parser = argparse.ArgumentParser(description="Search HuggingFace for models")
    parser.add_argument("--search", help="Search keyword")
    parser.add_argument("--mem", type=float, default=0, help="Available memory in GB")
    parser.add_argument("--format", help="Filter by model format (gguf, pytorch, safetensors, tensorflow, onnx, or leave empty for all)")
    parser.add_argument("--files", help="List model files in a repo")
    args = parser.parse_args()
    
    if args.search:
        search_models(args.search, args.mem, args.format)
    elif args.files:
        list_repo_files(args.files)
    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
