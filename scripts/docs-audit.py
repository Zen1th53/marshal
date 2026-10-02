"""Verify the public source boundary and scan every generated file for leaks."""
from pathlib import Path
import html
import importlib.util
import ipaddress
import json
import re
import sys
from urllib.parse import urlsplit

from mkdocs.config import load_config
from mkdocs.structure.files import get_files

root = Path(__file__).resolve().parent.parent
config = load_config(str(root / 'mkdocs.yml'))
errors = []
patterns = config['exclude_docs'].patterns
# Require the reviewed positive allowlist form, not a denylist or a nav-only gate.
if not patterns or str(patterns[0].pattern).strip() != '/**':
    errors.append('exclude_docs must start with /**')
allowlist = []
for pattern in patterns[1:]:
    entry = str(pattern.pattern).strip()
    # Pages, plus the site's own stylesheets; never a glob.
    if not re.fullmatch(r'!/[A-Za-z0-9_/-]+\.md|!/stylesheets/[A-Za-z0-9_-]+\.css', entry):
        errors.append('exclude_docs entries must be explicit Markdown or stylesheet paths')
    else:
        allowlist.append(entry[2:])
if not allowlist or len(set(allowlist)) != len(allowlist):
    errors.append('allowlist must be nonempty and unique')
for source in allowlist:
    if not (root / 'docs' / source).is_file():
        errors.append(f'missing allowed source: {source}')

files = get_files(config)
doc_files = list(files)
files.add_files_from_theme(config.theme.get_env(), config)
hook_spec = importlib.util.spec_from_file_location('docs_theme', root / 'scripts/docs-theme.py')
hook = importlib.util.module_from_spec(hook_spec)
hook_spec.loader.exec_module(hook)
files = hook.on_files(files, config)
published = [f for f in files if not f.inclusion.is_excluded()]
for file in published:
    if file in doc_files and file.src_uri not in allowlist:
        errors.append(f'non-allowlisted source would publish: {file.src_uri}')
expected_pages = {f.dest_uri for f in published if f.is_documentation_page()}
# Only theme's standard error page may be generated without a docs source.
expected_pages.add('404.html')
site = Path(config['site_dir'])
actual_pages = {p.relative_to(site).as_posix() for p in site.rglob('*.html')}
for extra in sorted(actual_pages - expected_pages):
    errors.append(f'page has no allowlisted source: {extra}')
for missing in sorted(expected_pages - actual_pages):
    errors.append(f'expected page missing: {missing}')
expected_files = {f.dest_uri for f in published}
expected_files.update({'404.html', 'sitemap.xml', 'sitemap.xml.gz', 'search/search_index.json'})
actual_files = {p.relative_to(site).as_posix() for p in site.rglob('*') if p.is_file()}
for extra in sorted(actual_files - expected_files):
    errors.append(f'file has no allowed source or generator: {extra}')
for missing in sorted(expected_files - actual_files):
    errors.append(f'expected file missing: {missing}')
# Excluded non-page sources must not be copied either.
for file in files:
    if file.inclusion.is_excluded() and (site / file.dest_uri).exists():
        errors.append(f'excluded source present in site: {file.src_uri}')

# Derive endpoint hosts from production repository code, including constants. Ban non-local HTTP hosts except the public release service.
endpoint_hosts = set()
for source in (root / 'internal').rglob('*.go'):
    if source.name.endswith('_test.go'):
        continue
    text = source.read_text()
    for url in re.findall(r'https?://[^\s"`\'<>]+', text):
        try:
            host = urlsplit(url).hostname
        except ValueError:
            continue
        if host and host not in {'127.0.0.1', '0.0.0.0', 'localhost', 'unix',
                                'github.com', 'api.github.com'}:
            endpoint_hosts.add(host.lower())
    # Cover bare host/domain literals in Cloud/backend code as well.
    if 'cloud' in source.as_posix().lower() or 'backend' in source.as_posix().lower():
        for literal in re.findall(r'"([^"\n]+)"', text):
            if re.fullmatch(r'(?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}(?::\d+)?', literal):
                endpoint_hosts.add(literal.split(':')[0].lower())

leak_patterns = {
    'local home path': r'/home/',
    'local installation path': r'/opt/',
    'private key': r'-----BEGIN (?:[A-Z0-9 ]*PRIVATE KEY|PGP PRIVATE KEY BLOCK)-----',
    'token prefix': r'(?<![A-Za-z0-9_])(?:ghp_|sk-|xox|AKIA)',
    'authorization value': r'\bBearer\s+[^\s<>"\']+',
    'installation identity': r'inst-[0-9a-f]{4}',
}
ipv4 = re.compile(r'(?<![\w.\-])(?:\d{1,3}\.){3}\d{1,3}(?![\w.\-])')
for path in sorted(site.rglob('*')):
    if not path.is_file():
        continue
    raw = path.read_bytes().decode('utf-8', errors='replace')
    if path.suffix == '.json':
        try:
            def json_strings(value):
                if isinstance(value, str):
                    yield value
                elif isinstance(value, dict):
                    for key, item in value.items():
                        yield key
                        yield from json_strings(item)
                elif isinstance(value, list):
                    for item in value:
                        yield from json_strings(item)
            raw += '\n' + '\n'.join(json_strings(json.loads(raw)))
        except json.JSONDecodeError:
            errors.append(f'{path.relative_to(site)}: invalid generated JSON')
    content = html.unescape(raw)
    # Search indexes can escape slashes and angle brackets.
    content = content.replace(r'\/', '/').replace(r'\u002f', '/')
    # Check visible text as well, so HTML formatting cannot split a value.
    content += '\n' + re.sub(r'<[^>]*>', '', content)
    relative = path.relative_to(site).as_posix()
    for label, pattern in leak_patterns.items():
        if re.search(pattern, content):
            errors.append(f'{relative}: {label}')
    for match in ipv4.finditer(content):
        try:
            address = ipaddress.IPv4Address(match.group())
        except ipaddress.AddressValueError:
            continue
        if str(address) not in {'127.0.0.1', '0.0.0.0'}:
            errors.append(f'{relative}: non-local IPv4')
            break
    for host in endpoint_hosts:
        if host in content.lower():
            errors.append(f'{relative}: internal endpoint host')
            break
if errors:
    # Do not echo potentially confidential matched values into CI logs.
    print('\n'.join('ERROR: ' + error for error in errors), file=sys.stderr)
    sys.exit(1)
print(f'Public docs audit passed: {len(allowlist)} allowed sources; all built files scanned.')
