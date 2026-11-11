#!/usr/bin/env python3
"""Check the actual APK boundary, independently of Gradle's source configuration."""
import json
import sys
import zipfile
from pathlib import Path

apk = Path(sys.argv[1]).resolve(strict=True)
with zipfile.ZipFile(apk) as archive:
    entries = archive.namelist()
    forbidden = [entry for entry in entries if entry.startswith('assets/public/')
                 or entry.endswith(('capacitor.config.json', 'capacitor.plugins.json'))
                 or (entry.startswith('lib/') and entry.endswith('.so'))]
    if forbidden:
        raise SystemExit(f'Unexpected bundled website/browser/runtime entries: {forbidden}')
    dex = [entry for entry in entries if entry.endswith('.dex')]
    if len(dex) != 1:
        raise SystemExit(f'Expected one compact DEX, got {len(dex)}')
    largest = sorted(((item.filename, item.file_size) for item in archive.infolist()),
                     key=lambda item: item[1], reverse=True)[:10]
size = apk.stat().st_size
print(json.dumps({'apk_bytes': size, 'apk_mib': round(size / 1048576, 3),
                  'budget_bytes': 5 * 1048576, 'budget_pass': size <= 5 * 1048576,
                  'dex_files': dex, 'largest_uncompressed_entries': largest}, indent=2))
if size > 5 * 1048576:
    raise SystemExit('APK exceeds the 5 MiB engineering budget; investigate the reported entries')
