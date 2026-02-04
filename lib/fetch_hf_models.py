import sys, json, re

try:
    from urllib.request import urlopen, Request
except ImportError:
    sys.exit(1)

cache_path = sys.argv[1]
orgs = sys.argv[2].split()
total_mem_gb = float(sys.argv[3] or "8")

API = "https://huggingface.co/api/models"
QUANT_RE = re.compile(r'[Qq]\d[_.][Kk]?_?[A-Za-z0-9]*|IQ\d[_.][A-Za-z0-9]*')
SHARD_RE = re.compile(r'-\d{5}-of-\d{5}')
PARAM_RE = re.compile(r'(\d+(?:\.\d+)?)\s*[Bb](?:illion)?(?:\b|_)')

def estimate_q4_gb(billions):
    """Estimate Q4_K_M file size in GB from param count in billions."""
    return round(billions * 0.6 + 0.5, 1)

def parse_param_billions(name):
    """Extract parameter count in billions from model name, e.g. '8B' -> 8.0"""
    m = PARAM_RE.search(name)
    if m:
        return float(m.group(1))
    return None

# Pick search size buckets based on total memory
# Rule of thumb: Q4_K_M ≈ 0.6 GB per billion params, plus ~1 GB overhead
size_tags = ["1b", "3b"]
if total_mem_gb >= 8:
    size_tags += ["7b", "8b"]
if total_mem_gb >= 16:
    size_tags += ["14b"]
if total_mem_gb >= 32:
    size_tags += ["30b", "32b", "34b"]
if total_mem_gb >= 64:
    size_tags += ["70b", "72b"]

# Max param count (billions) that fits in total memory at Q4_K_M
max_param_b = (total_mem_gb - 1) / 0.6

def api_get(url):
    req = Request(url, headers={"User-Agent": "oi-cli/1.0"})
    with urlopen(req, timeout=15) as resp:
        return json.loads(resp.read().decode())

seen = {}

for org in orgs:
    for size in size_tags:
        url = (f"{API}?author={org}&search={size}+gguf"
               f"&sort=downloads&direction=-1&limit=5&filter=text-generation")
        try:
            results = api_get(url)
        except Exception:
            continue

        for repo_info in results:
            repo_id = repo_info.get("id", "")
            downloads = repo_info.get("downloads", 0)
            likes = repo_info.get("likes", 0)

            # Estimate param count from repo name and check it fits
            param_b = parse_param_billions(repo_id)
            if param_b and param_b > max_param_b:
                continue

            try:
                details = api_get(f"{API}/{repo_id}")
            except Exception:
                continue

            siblings = details.get("siblings", [])

            # Collect single-file GGUFs only (skip sharded multi-part files)
            gguf_files = []
            for s in siblings:
                fn = s.get("rfilename", "")
                if not fn.endswith(".gguf") or fn.startswith("."):
                    continue
                if SHARD_RE.search(fn):
                    continue  # sharded — skip
                gguf_files.append(s)

            if not gguf_files:
                continue

            # Pick representative file (prefer Q4_K_M)
            rep = None
            for s in gguf_files:
                fn = s["rfilename"]
                if "Q4_K_M" in fn.upper().replace("-", "_"):
                    rep = fn
                    break
            if not rep:
                rep = gguf_files[0]["rfilename"]

            # Build filename template
            m = QUANT_RE.search(rep)
            if m:
                template = rep[:m.start()] + "{quant}" + rep[m.end():]
            else:
                template = rep

            # Estimate min_vram_gb from param count
            if param_b:
                min_vram = estimate_q4_gb(param_b)
            else:
                min_vram = 3.0

            # Derive short CLI id
            repo_name = repo_id.split("/")[-1]
            cli_id = repo_name.lower()
            cli_id = re.sub(r'-gguf$', '', cli_id)
            cli_id = re.sub(r'-instruct', '', cli_id)
            cli_id = re.sub(r'[._]', '-', cli_id)
            cli_id = re.sub(r'-+', '-', cli_id).strip('-')

            # Deduplicate: keep highest download count
            if cli_id in seen:
                if seen[cli_id]["_downloads"] >= downloads:
                    continue

            model_name = repo_name.replace("-GGUF", "").replace("-gguf", "")

            seen[cli_id] = {
                "id": cli_id,
                "name": model_name,
                "repo": repo_id,
                "filename_template": template,
                "min_vram_gb": min_vram,
                "description": f"{downloads:,} downloads, {likes:,} likes on HuggingFace",
                "tags": ["dynamic", org.lower()],
                "_downloads": downloads
            }

models = sorted(seen.values(), key=lambda m: m["_downloads"], reverse=True)
for m in models:
    del m["_downloads"]

output = {"models": models}
with open(cache_path, "w") as f:
    json.dump(output, f, indent=2)

print(f"Fetched {len(models)} models from HuggingFace", file=sys.stderr)
