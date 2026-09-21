#!/usr/bin/env python3
"""Checks benchmark-client selection without building every server."""
import os
from pathlib import Path
import subprocess
import unittest


ROOT = Path(__file__).resolve().parent.parent


def selected(extra_env=None):
    env = os.environ.copy()
    env.pop("BENCH_CLIENT", None)
    if extra_env:
        env.update(extra_env)
    return subprocess.run(
        ["bash", "-c", ". ./script/config.sh && printf '%s' \"$BENCH_CLIENT\""],
        cwd=ROOT,
        env=env,
        text=True,
        capture_output=True,
        timeout=10,
    )


class ScriptTests(unittest.TestCase):
    def test_cpp_is_default(self):
        result = selected()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "benchcli-uwscpp")

    def test_go_can_be_selected(self):
        result = selected({"BENCH_CLIENT": "benchcli-go"})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "benchcli-go")

    def test_unknown_client_fails(self):
        result = selected({"BENCH_CLIENT": "unknown"})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Unsupported BENCH_CLIENT", result.stderr)

    def test_framework_subset(self):
        result = subprocess.run(
            ["bash", "-c", ". ./script/config.sh && printf '%s' \"${frameworks[*]}\""],
            cwd=ROOT,
            env={**os.environ, "BENCH_FRAMEWORKS": "gorilla,nbio_nonblocking"},
            text=True,
            capture_output=True,
            timeout=10,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "gorilla nbio_nonblocking")

    def test_unknown_framework_fails(self):
        result = subprocess.run(
            ["bash", "-c", ". ./script/config.sh"],
            cwd=ROOT,
            env={**os.environ, "BENCH_FRAMEWORKS": "unknown"},
            text=True,
            capture_output=True,
            timeout=10,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Unsupported framework", result.stderr)

    def test_docker_runner_help_without_daemon(self):
        result = subprocess.run(
            ["bash", "script/docker_benchmark.sh", "--help"],
            cwd=ROOT,
            text=True,
            capture_output=True,
            timeout=10,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("DOCKER_BENCH_CPUS", result.stdout)

    def test_build_dispatch_contains_both_clients(self):
        source = (ROOT / "script/build.sh").read_text()
        self.assertIn("go build -o ./output/bin/bench.client ./benchcli-go", source)
        self.assertIn("bash ./benchcli-uwscpp/build.sh", source)


if __name__ == "__main__":
    unittest.main(verbosity=2)
