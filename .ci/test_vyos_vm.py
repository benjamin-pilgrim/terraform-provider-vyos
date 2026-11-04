import hashlib
import io
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import vyos_vm


class ImageTests(unittest.TestCase):
    def test_download_and_cached_image_are_verified(self):
        data = b'pinned fixture ISO bytes'
        with tempfile.TemporaryDirectory() as folder:
            cache = Path(folder)
            with patch.object(vyos_vm, 'SHA256', hashlib.sha256(data).hexdigest()):
                with patch('vyos_vm.urllib.request.urlopen', return_value=io.BytesIO(data)) as download:
                    image = vyos_vm.fetch(cache)
                    self.assertEqual(image.read_bytes(), data)
                    self.assertFalse(image.with_suffix('.part').exists())
                    self.assertEqual(vyos_vm.fetch(cache), image)
                    self.assertEqual(download.call_count, 1)
                image.write_bytes(b'tampered')
                with patch('vyos_vm.urllib.request.urlopen') as download:
                    with self.assertRaisesRegex(RuntimeError, 'checksum mismatch'):
                        vyos_vm.fetch(cache)
                    download.assert_not_called()

    def test_bad_download_is_not_published_or_left_behind(self):
        with tempfile.TemporaryDirectory() as folder:
            with patch('vyos_vm.urllib.request.urlopen', return_value=io.BytesIO(b'bad download')):
                with self.assertRaisesRegex(RuntimeError, 'checksum mismatch'):
                    vyos_vm.fetch(Path(folder))
            self.assertEqual(list(Path(folder).iterdir()), [])

    def test_corrupt_image_cannot_reach_vm_start(self):
        with tempfile.TemporaryDirectory() as folder:
            cache = Path(folder)
            (cache / vyos_vm.NAME).write_bytes(b'corrupt')
            args = type('Args', (), {'output': cache / 'logs', 'cache': cache})()
            with patch('vyos_vm.subprocess.Popen') as start:
                with self.assertRaisesRegex(RuntimeError, 'checksum mismatch'):
                    vyos_vm.run(args)
                start.assert_not_called()


class IsolationTests(unittest.TestCase):
    def test_only_loopback_https_forward_is_exposed(self):
        command = vyos_vm.qemu_command(Path('/image.iso'), Path('/boot'), Path('/seed.iso'),
                                       Path('/state'), 19443, 'tcg')
        network = command[command.index('-netdev') + 1]
        self.assertEqual(command.count('-netdev'), 1)
        self.assertIn('restrict=on', network)
        self.assertIn('hostfwd=tcp:127.0.0.1:19443-10.0.2.15:443', network)
        self.assertNotIn('tap', network)
        self.assertNotIn('-enable-kvm', command)
        disks = [command[i + 1] for i, arg in enumerate(command) if arg == '-drive']
        self.assertEqual(len(disks), 2)
        self.assertTrue(all('media=cdrom,readonly=on' in disk for disk in disks))
        self.assertIn('nopersistence', command[command.index('-append') + 1])


if __name__ == '__main__':
    unittest.main()
