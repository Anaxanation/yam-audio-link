from pathlib import Path
from datetime import datetime, timedelta, timezone
import hashlib
import json
import shutil
import zipfile

root = Path(__file__).resolve().parent
repo = root / 'publish'
docs = repo / 'docs'
catalog_path = docs / 'catalog'
packages_path = docs / 'packages'
catalog_path.mkdir(parents=True, exist_ok=True)
packages_path.mkdir(parents=True, exist_ok=True)

def text(ru, en):
    return {'ru': ru, 'en': en}

elf = (root / 'audio-link-linux-arm64').read_bytes()
assert elf[:7] == b'\x7fELF\x02\x01\x01' and elf[18:20] == b'\xb7\x00'
manifest = {
    'format': 1, 'id': 'anaxanation.audio-link', 'version': '0.2.1', 'api': 2,
    'engine': 'native', 'title': text('Audio Link', 'Audio Link'),
    'description': text('Передача PCM между колонками. Захват штатного звука требует ALSA Loopback.',
                        'PCM relay between speakers. Native output capture requires ALSA Loopback.'),
    'author': 'Anaxanation', 'devices': ['yandexmini'],
    'permissions': ['system.full-access', 'log.write'],
    'capabilities': ['network.local'],
    'config': [
        {'key': 'mode', 'label': text('Режим', 'Mode'), 'type': 'select', 'default': 'recv', 'options': [
            {'value': 'recv', 'label': text('Приём', 'Receive')},
            {'value': 'test', 'label': text('Тестовый сигнал', 'Test tone')},
            {'value': 'send', 'label': text('Передача выхода ALSA', 'Send ALSA output')},
        ]},
        {'key': 'peer', 'label': text('IPv4 второй колонки', 'Peer IPv4 address'), 'type': 'text', 'default': '', 'max_length': 15},
        {'key': 'port', 'label': text('Порт UDP', 'UDP port'), 'type': 'range', 'default': 47668, 'min': 1, 'max': 65535, 'step': 1},
        {'key': 'pcm', 'label': text('ALSA PCM, пусто для автовыбора', 'ALSA PCM, blank for default'), 'type': 'text', 'default': '', 'max_length': 128},
    ],
    'module_sha256': hashlib.sha256(elf).hexdigest(),
}
package = root / 'audio-link.aliceplugin'
with zipfile.ZipFile(package, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
    archive.writestr('manifest.json', json.dumps(manifest, ensure_ascii=False))
    archive.writestr('plugin.elf', elf)
    archive.write(root / 'README.md', 'README.md')
with zipfile.ZipFile(package) as archive:
    assert archive.testzip() is None
    assert hashlib.sha256(archive.read('plugin.elf')).hexdigest() == manifest['module_sha256']

digest = hashlib.sha256(package.read_bytes()).hexdigest()
name = f'{digest}.aliceplugin'
shutil.copyfile(package, packages_path / name)
stamp = datetime.now(timezone.utc).replace(microsecond=0)
def iso(value):
    return value.isoformat().replace('+00:00', 'Z')

catalog = {'format': 1, 'revision': 2, 'generated_at': iso(stamp),
           'expires_at': iso(stamp + timedelta(days=365)), 'revoked': [],
           'entries': [{'manifest': manifest, 'sha256': digest, 'bytes': package.stat().st_size,
                        'download': f'../packages/{name}', 'reviewed_at': iso(stamp)}]}
(root / 'catalog-payload.json').write_bytes(json.dumps(catalog, ensure_ascii=False).encode('utf-8'))
for name in ('README.md', 'main.go'):
    shutil.copyfile(root / name, repo / name)
print(f'ELF={len(elf)}, aliceplugin={package.stat().st_size}, package_sha256={digest}')
