#!/usr/bin/env python3
"""Extract topic-specific keywords from the curated corpus and feed them back
into a goCrawler profile. Optionally harvest candidate URLs from the rejected
link logs or from an external file.

This implements the iterative loop:

    crawl -> curated corpus -> keywords -> updated profile -> crawl again

Examples:
    python3 tools/corpus_feedback.py --profile profiles/skystacker-research.yaml
    python3 tools/corpus_feedback.py --profile profiles/skystacker-research.yaml \
        --update --harvest-rejected --urls-file /tmp/new_urls.txt
"""

import argparse
import collections
import pathlib
import re
import sys

TOKEN_RE = re.compile(r"[a-z][a-z0-9\-]{2,}")
URL_RE = re.compile(r"https?://[^\s\"'<>)]+")

STOPWORDS = set("""
a about above after again against all also am an and any are as at be because been before being
below between both but by can could did do does doing down during each few for from further had has
have having he her here hers herself him himself his how i if in into is it its itself just me more
most my myself no nor not now of off on once only or other our ours ourselves out over own same she
should so some such than that the their theirs them themselves then there these they this those
through to too under until up very was we were what when where which while who whom why will with
you your yours yourself yourselves com www http https html php aspx index en january february march
april may june july august september october november december org gov net edu co uk us ca
using used use one two three value values set option options time first last default name number
data file files field fields tab tabs see example examples program output input within small large
contains contain try people provide process current different work web service page pages site sites
format line lines code version command commands run start end based make made need want get got
also each other more most much many some such only own same too very here there where when how why
""".split())


def parse_args():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--corpus", default="logs/corpusCuratedText.log")
    parser.add_argument("--baseline", default="corpusfreqlib/all.num")
    parser.add_argument("--profile", default="profiles/skystacker-research.yaml")
    parser.add_argument("--report", default="output/corpus_keywords_report.txt")
    parser.add_argument("--top", type=int, default=40)
    parser.add_argument("--min-docs", type=int, default=2)
    parser.add_argument("--contrast", type=float, default=20.0)
    parser.add_argument("--max-seeds", type=int, default=20)
    parser.add_argument("--urls-file", default=None)
    parser.add_argument("--harvest-rejected", action="store_true")
    parser.add_argument("--update", action="store_true",
                        help="write keywords and candidate URLs into the profile")
    return parser.parse_args()


def load_documents(path):
    """Split the curated corpus into (url, text) pairs."""
    docs = []
    url, chunks = None, []
    for line in pathlib.Path(path).read_text(errors="ignore").splitlines():
        if line.startswith("http"):
            if url:
                docs.append((url, " ".join(chunks)))
            url, chunks = line.strip(), []
        elif url and line.strip() and line.strip() != "----":
            chunks.append(line.strip())
    if url:
        docs.append((url, " ".join(chunks)))
    return docs


def load_baseline(path):
    """Load British National Corpus frequencies: 'freq word POS code'."""
    freqs = {}
    for line in pathlib.Path(path).read_text(errors="ignore").splitlines():
        parts = line.split()
        if len(parts) >= 2 and parts[1] not in ("!!WHOLE_CORPUS",):
            try:
                freqs[parts[1].lower()] = int(parts[0])
            except ValueError:
                pass
    return freqs


def extract_keywords(docs, baseline, top, min_docs, contrast):
    """Rank tokens by topic specificity using intercorpus contrast."""
    counts = collections.Counter()
    doc_freq = collections.Counter()
    total = 0
    for _, text in docs:
        tokens = [t for t in TOKEN_RE.findall(text.lower()) if t not in STOPWORDS]
        counts.update(tokens)
        doc_freq.update(set(tokens))
        total += len(tokens)

    the_base = baseline.get("the", 1)
    scale = (1 + the_base) / max(1, counts.get("the", 1))
    ranked = []
    for word, count in counts.items():
        if doc_freq[word] < min_docs or word == "the":
            continue
        expected = contrast * (1 + baseline.get(word, 0)) / scale
        score = count - expected
        if score > 0:
            ranked.append((score, word, count, doc_freq[word]))
    ranked.sort(reverse=True)
    return ranked[:top], total


def read_block(lines, key):
    """Return (start, end, tokens) for a YAML literal block, preserving order."""
    for i, line in enumerate(lines):
        if line.startswith(key + ":"):
            j = i + 1
            tokens = []
            while j < len(lines) and (lines[j].startswith(" ") or not lines[j].strip()):
                if lines[j].strip():
                    tokens.extend(lines[j].strip().split())
                j += 1
            return i, j, list(dict.fromkeys(tokens))
    return None, None, []


def write_block(lines, key, tokens):
    """Replace a YAML literal block with a single indented line of tokens."""
    start, end, _ = read_block(lines, key)
    if start is None:
        return lines
    header = lines[start].split(":")[0] + ": |"
    return lines[:start] + [header, "  " + "|".join(tokens)] + lines[end:]


def regex_terms(lines, key):
    """Extract the inner alternatives of a (?i)\\W(...)\\W regex block."""
    _, _, tokens = read_block(lines, key)
    value = "".join(tokens)
    prefix, suffix = r"(?i)\W(", r")\W"
    if value.startswith(prefix) and value.endswith(suffix):
        value = value[len(prefix):-len(suffix)]
    return list(dict.fromkeys(t for t in value.split("|") if t))


def write_regex_block(lines, key, terms):
    """Write a regex literal block keeping its (?i)\\W(...) wrapper."""
    start, end, _ = read_block(lines, key)
    if start is None:
        return lines
    header = lines[start].split(":")[0] + ": |"
    value = r"(?i)\W(" + "|".join(terms) + r")\W"
    return lines[:start] + [header, "  " + value] + lines[end:]


JUNK_DOMAINS = ("blogger.com", "blogspot.com", "wp.com", "agenaastro.com",
                "googleusercontent.com", "facebook.com", "twitter.com", "pinterest.com",
                "youtube.com", "github.com", "files.kde.org", "mail.kde.org")
JUNK_EXTENSIONS = (".jpg", ".jpeg", ".png", ".gif", ".svg", ".webp", ".pdf", ".zip",
                   ".deb", ".dmg", ".exe", ".mp4", ".css", ".js", ".gz", ".tar",
                   ".mirrorlist", ".xml")


def is_candidate(url, keywords, existing):
    """Reject tracking links, binary assets and commerce pages."""
    low = url.lower()
    if url in existing or len(url) > 200:
        return False
    if any(c in low for c in ("?", "&", "=", "#", "/wp-content/uploads/", "%",
                              "/download", "sourceforge.net")):
        return False
    if any(dom in low for dom in JUNK_DOMAINS):
        return False
    if any(low.endswith(ext) for ext in JUNK_EXTENSIONS):
        return False
    return sum(1 for k in keywords if k in low) >= 2


def harvest_urls(keywords, args, existing):
    """Collect candidate URLs containing the new keywords."""
    candidates = []
    sources = []
    if args.harvest_rejected:
        sources.extend(sorted(pathlib.Path("logs").glob("linksNotOk*.log")))
    if args.urls_file:
        sources.append(pathlib.Path(args.urls_file))
    for path in sources:
        if not path.exists():
            continue
        for url in URL_RE.findall(path.read_text(errors="ignore")):
            url = url.rstrip(".,;")
            if is_candidate(url, keywords, existing):
                existing.add(url)
                candidates.append(url)
    return candidates[:args.max_seeds]


def main():
    args = parse_args()
    corpus = pathlib.Path(args.corpus)
    if not corpus.exists():
        print("Corpus not found:", corpus, file=sys.stderr)
        return 1

    docs = load_documents(corpus)
    baseline = load_baseline(args.baseline)
    ranked, total_tokens = extract_keywords(docs, baseline, args.top,
                                            args.min_docs, args.contrast)

    print(f"Documents: {len(docs)}  Tokens: {total_tokens}")
    print("Top topic-specific keywords (score, word, count, docs):")
    for score, word, count, df in ranked:
        print(f"  {int(score):6}  {word:22} {count:5} {df:4}")
    keywords = [word for _, word, _, _ in ranked]

    profile_path = pathlib.Path(args.profile)
    lines = profile_path.read_text().splitlines() if profile_path.exists() else []
    report = ["# Corpus keyword feedback", "", f"Corpus: {corpus}",
              f"Documents: {len(docs)}  Tokens: {total_tokens}", "",
              "## Ranked keywords", ""]
    report += [f"{int(s)}\t{w}\t{c}\t{d}" for s, w, c, d in ranked]

    if args.update and lines:
        link_tokens = read_block(lines, "linkOk")[2]
        ranking_tokens = regex_terms(lines, "regexRankingKeywords")
        new_link = [k for k in keywords if k not in link_tokens]
        new_rank = [k for k in keywords if k not in ranking_tokens]
        lines = write_block(lines, "linkOk", link_tokens + new_link)
        lines = write_regex_block(lines, "regexRankingKeywords", ranking_tokens + new_rank)

        existing_urls = set(re.findall(r'"(https?://[^"]+)"', "\n".join(lines)))
        urls = harvest_urls(keywords, args, existing_urls)
        if urls:
            lines.append("")
            lines.append("# Candidate URLs discovered by corpus_feedback.py")
            lines.extend(f'- "{url}"' for url in urls)
        profile_path.write_text("\n".join(lines) + "\n")
        report += ["", "## Profile updated", f"linkOk additions: {len(new_link)}",
                   f"regexRankingKeywords additions: {len(new_rank)}",
                   f"candidate URLs added: {len(urls)}", ""]
        report += [f"- {u}" for u in urls]
    else:
        report += ["", "(dry run: profile not modified; use --update)"]

    out = pathlib.Path(args.report)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text("\n".join(report) + "\n")
    print("Report:", out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
