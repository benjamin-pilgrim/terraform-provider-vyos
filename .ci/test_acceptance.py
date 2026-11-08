import os
from pathlib import Path
import signal
import shutil
import subprocess
import sys
import tempfile
import time
import unittest


class CancellationTests(unittest.TestCase):
    def test_wrapper_forwards_cancellation_and_waits_for_cleanup(self):
        script = Path(__file__).with_name('acceptance.sh').resolve()
        for cancel in [signal.SIGTERM, signal.SIGINT]:
            with self.subTest(signal=cancel), tempfile.TemporaryDirectory() as folder:
                root = Path(folder)
                (root / 'go').write_text('#!/bin/sh\nexit 0\n')
                (root / 'go').chmod(0o700)
                # Stand in for the runner without downloading or booting a VM.
                (root / 'python3').write_text(f'#!{sys.executable}\n' + '''import os
from pathlib import Path
import signal, sys, threading, time
root = Path(os.environ['PROBE_FOLDER'])
work = Path(sys.argv[sys.argv.index('--test-binary') + 1]).parent
stopped = threading.Event()
signal.signal(signal.SIGTERM, lambda *_: stopped.set())
(root / 'work').write_text(str(work))
(root / 'ready').write_text(str(os.getpid()))
if stopped.wait(30):
    time.sleep(.1)
    (root / 'cleaned').write_text(str(work.is_dir()))
''')
                (root / 'python3').chmod(0o700)
                env = os.environ | {'PATH': str(root) + os.pathsep + os.environ['PATH'],
                                    'PROBE_FOLDER': str(root), 'VYOS_CI_BUILD': str(root / 'build.json'),
                                    'TF_ACC_TERRAFORM_PATH': '/unused/terraform'}
                process = subprocess.Popen(['bash', str(script)], env=env, start_new_session=True,
                                           stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                try:
                    deadline = time.monotonic() + 5
                    while not (root / 'ready').exists():
                        if process.poll() is not None or time.monotonic() > deadline:
                            self.fail('wrapper did not start the runner')
                        time.sleep(.02)
                    child = int((root / 'ready').read_text())
                    process.send_signal(cancel)
                    process.communicate(timeout=5)
                    self.assertEqual(process.returncode, 128 + cancel)
                    self.assertEqual((root / 'cleaned').read_text(), 'True',
                                     'temporary files removed before runner cleanup')
                    self.assertFalse(Path((root / 'work').read_text()).exists())
                    with self.assertRaises(ProcessLookupError):
                        os.kill(child, 0)
                finally:
                    # Keep a failing regression run from leaving its stub behind.
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    process.communicate(timeout=5)
                    if (root / 'work').exists():
                        shutil.rmtree((root / 'work').read_text(), ignore_errors=True)


if __name__ == '__main__':
    unittest.main()
