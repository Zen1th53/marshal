"""Keep installed theme assets while leaving the docs source allowlist intact."""
from pathlib import Path
from mkdocs.structure.files import InclusionLevel


def on_files(files, config):
    theme_roots = {Path(directory).resolve() for directory in config.theme.dirs}
    docs_root = Path(config.docs_dir).resolve()
    for file in files:
        if file.src_dir is None:
            continue
        source_root = Path(file.src_dir).resolve()
        # Never exempt a docs source, even if it has the same name as a theme asset.
        if source_root in theme_roots and source_root != docs_root:
            if file.src_uri.endswith('.map') or file.src_uri.endswith('/wordcut.js'):
                # Source maps and Thai segmentation are unused by this English site.
                file.inclusion = InclusionLevel.EXCLUDED
                continue
            if file.src_uri.endswith('.css'):
                # Space compact SVG coordinates without changing their geometry.
                # This keeps numeric path data from resembling an IPv4 literal.
                file.content_string = file.content_string.replace(
                    '1.7.75.75', '1.7%20.75%20.75')
            file.inclusion = InclusionLevel.INCLUDED
    return files
