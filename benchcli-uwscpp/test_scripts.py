#!/usr/bin/env python3
"""Checks benchmark-client selection without building every server."""
import os
from pathlib import Path
import re
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

    # Every framework list is kept in framework-name order, and the shell ones
    # carry names config.FrameworkList knows - which is what a report's rows are
    # ordered by. A subset rather than the whole list, since commenting
    # frameworks out of script/config.sh is how a focused run is set up.
    def test_framework_lists_are_sorted_and_match_go(self):
        result = subprocess.run(
            ["bash", "-c", ". ./script/config.sh && printf '%s\n' \"${frameworks[*]}\" \"${taskpool_frameworks[*]}\""],
            cwd=ROOT,
            env={k: v for k, v in os.environ.items() if k != "BENCH_FRAMEWORKS"},
            text=True,
            capture_output=True,
            timeout=10,
        )
        self.assertEqual(result.returncode, 0, result.stderr)

        config = (ROOT / "config/config.go").read_text()
        names = dict(re.findall(r'^\s*(\w+)\s*=\s*"([^"\n]+)"', config, re.M))
        order = re.search(r"var FrameworkList = \[\]string\{(.*?)\}", config, re.S).group(1)
        go = [names[name] for name in re.findall(r"(\w+)\s*,", order)]
        self.assertEqual(go, sorted(go))

        subset = (ROOT / "script/1m_conns_benchmark.sh").read_text()
        million = re.findall(r'^\s*"([a-z0-9_]+)"$', re.search(
            r"frameworks=\((.*?)\)", subset, re.S).group(1), re.M)
        shell, taskpool = (line.split() for line in result.stdout.splitlines())
        for name, listed in [("frameworks", shell), ("taskpool_frameworks", taskpool),
                             ("1m_conns_benchmark.sh frameworks", million)]:
            self.assertEqual(listed, sorted(listed), name)
            self.assertTrue(listed and set(listed) <= set(go), (name, listed))

    def test_build_dispatch_contains_both_clients(self):
        source = (ROOT / "script/build.sh").read_text()
        self.assertIn("go build -o ./output/bin/bench.client ./benchcli-go", source)
        self.assertIn("bash ./benchcli-uwscpp/build.sh", source)


if __name__ == "__main__":
    unittest.main(verbosity=2)
