import hashlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import vyos_vm


BUILD = vyos_vm.Build("2026.10.22-0026-rolling", "0" * 64)


class ImageTests(unittest.TestCase):
    def test_download_and_cached_image_are_verified(self):
        data = b'pinned fixture ISO bytes'
        with tempfile.TemporaryDirectory() as folder:
            cache = Path(folder)
            build = vyos_vm.Build(BUILD.version, hashlib.sha256(data).hexdigest())
            with patch('vyos_vm.urllib.request.urlopen', return_value=io.BytesIO(data)) as download:
                image = vyos_vm.fetch(cache, build)
                self.assertEqual(image.read_bytes(), data)
                self.assertFalse(image.with_suffix('.part').exists())
                self.assertEqual(vyos_vm.fetch(cache, build), image)
                self.assertEqual(download.call_count, 1)
            image.write_bytes(b'tampered')
            with patch('vyos_vm.urllib.request.urlopen') as download:
                with self.assertRaisesRegex(RuntimeError, 'checksum mismatch'):
                    vyos_vm.fetch(cache, build)
                download.assert_not_called()

    def test_bad_download_is_not_published_or_left_behind(self):
        with tempfile.TemporaryDirectory() as folder:
            with patch('vyos_vm.urllib.request.urlopen', return_value=io.BytesIO(b'bad download')):
                with self.assertRaisesRegex(RuntimeError, 'checksum mismatch'):
                    vyos_vm.fetch(Path(folder), BUILD)
            self.assertEqual(list(Path(folder).iterdir()), [])

    def test_corrupt_image_cannot_reach_vm_start(self):
        with tempfile.TemporaryDirectory() as folder:
            cache = Path(folder)
            (cache / BUILD.name).write_bytes(b'corrupt')
            BUILD.write(cache / 'build.json')
            args = type('Args', (), {'output': cache / 'logs', 'cache': cache, 'build': cache / 'build.json'})()
            with patch('vyos_vm.subprocess.Popen') as start:
                with self.assertRaisesRegex(RuntimeError, 'checksum mismatch'):
                    vyos_vm.run(args)
                start.assert_not_called()
                self.assertFalse(json.loads((cache / 'logs/result.json').read_text())['passed'])

    def test_missing_kvm_fails_before_vm_start(self):
        with tempfile.TemporaryDirectory() as folder:
            BUILD.write(Path(folder) / 'build.json')
            args = type('Args', (), {'output': Path(folder), 'cache': Path(folder), 'build': Path(folder) / 'build.json'})()
            with patch('vyos_vm.fetch'), patch('vyos_vm.os.access', return_value=False):
                with patch('vyos_vm.subprocess.Popen') as start:
                    with self.assertRaisesRegex(RuntimeError, 'KVM is required'):
                        vyos_vm.run(args)
                    start.assert_not_called()
                    result = json.loads((Path(folder) / 'result.json').read_text())
                    self.assertEqual(result, {'passed': False, 'phase': 'preflight', 'vm_stopped': True})

    def test_retry_replaces_stale_success_on_preflight_failure(self):
        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder)
            (output / 'result.json').write_text('{"passed":true}')
            args = type('Args', (), {'output': output, 'cache': output, 'build': output / 'missing.json'})()
            with self.assertRaises(FileNotFoundError):
                vyos_vm.run(args)
            self.assertFalse(json.loads((output / 'result.json').read_text())['passed'])


class ReleaseTests(unittest.TestCase):
    def metadata(self):
        return {'tag_name': BUILD.version, 'draft': False, 'prerelease': False,
                'assets': [{'name': BUILD.name, 'digest': 'sha256:' + BUILD.sha256,
                            'browser_download_url': BUILD.url}]}

    def resolve(self, metadata, release='latest'):
        with patch('vyos_vm.urllib.request.urlopen', return_value=io.BytesIO(json.dumps(metadata).encode())) as request:
            result = vyos_vm.resolve_build(release)
            self.assertTrue(request.call_args.args[0].full_url.endswith('/' + ('latest' if release == 'latest' else 'tags/' + release)))
            return result

    def test_latest_resolves_to_exact_version_and_digest(self):
        self.assertEqual(self.resolve(self.metadata()), BUILD)
        self.assertEqual(self.resolve(self.metadata(), BUILD.version), BUILD)
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / 'build.json'
            BUILD.write(path)
            self.assertEqual(vyos_vm.Build.read(path), BUILD)

    def test_missing_digest_or_iso_fails(self):
        metadata = self.metadata()
        metadata['assets'][0]['digest'] = None
        with self.assertRaisesRegex(ValueError, 'SHA256'):
            self.resolve(metadata)
        metadata['assets'] = []
        with self.assertRaisesRegex(ValueError, 'exactly one'):
            self.resolve(metadata)

    def test_wrong_version_url_or_unpublished_release_fails(self):
        metadata = self.metadata()
        metadata['tag_name'] = 'latest'
        with self.assertRaises(ValueError):
            self.resolve(metadata)
        metadata = self.metadata()
        metadata['assets'][0]['browser_download_url'] = 'https://example.com/image.iso'
        with self.assertRaisesRegex(ValueError, 'download URL'):
            self.resolve(metadata)
        metadata = self.metadata()
        metadata['draft'] = True
        with self.assertRaisesRegex(ValueError, 'published'):
            self.resolve(metadata)

    def test_manifest_rejects_unsafe_version_and_malformed_digest(self):
        for version, digest in [('../bad', BUILD.sha256), (BUILD.version, 'bad')]:
            with self.assertRaises(ValueError):
                vyos_vm.Build(version, digest)


class IsolationTests(unittest.TestCase):
    def test_only_loopback_https_forward_is_exposed(self):
        command = vyos_vm.qemu_command(Path('/image.iso'), Path('/boot'), Path('/seed.iso'),
                                       Path('/state'), 19443)
        network = command[command.index('-netdev') + 1]
        self.assertEqual(command.count('-netdev'), 1)
        self.assertIn('restrict=on', network)
        self.assertIn('hostfwd=tcp:127.0.0.1:19443-10.0.2.15:443', network)
        self.assertNotIn('tap', network)
        self.assertEqual(command[command.index('-accel') + 1], 'kvm')
        disks = [command[i + 1] for i, arg in enumerate(command) if arg == '-drive']
        self.assertEqual(len(disks), 2)
        self.assertTrue(all('media=cdrom,readonly=on' in disk for disk in disks))
        self.assertIn('nopersistence', command[command.index('-append') + 1])


if __name__ == '__main__':
    unittest.main()
