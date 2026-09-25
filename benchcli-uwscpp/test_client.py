#!/usr/bin/env python3
"""End-to-end tests against a small RFC 6455 fixture; no Python packages needed."""
import asyncio
import base64
import hashlib
import json
import os
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
import threading
import time
import unittest

BINARY = str(Path(sys.argv.pop(1) if len(sys.argv) > 1 else Path(__file__).resolve().parent.parent / 'output/bin/bench.client').resolve())


def frame(data, opcode=2, fin=True):
    first = (128 if fin else 0) | opcode
    n = len(data)
    header = bytes([first, n]) if n < 126 else (bytes([first, 126]) + struct.pack('!H', n) if n < 65536 else bytes([first, 127]) + struct.pack('!Q', n))
    return header + data


class Fixture:
    def __init__(self):
        self.loop = asyncio.new_event_loop()
        self.mode = 'echo'
        self.requests = []
        self.connections = 0
        self.attempts = 0
        self.masked = True
        self.ready = threading.Event()
        self.error = None
        self.thread = threading.Thread(target=self.run, daemon=True)
        self.thread.start()
        self.ready.wait(10)
        if self.error:
            raise self.error

    def run(self):
        asyncio.set_event_loop(self.loop)
        async def start():
            self.servers = []
            # gorilla's ports, and uwebsockets' for a framework with no pprof routes.
            for port in [*range(12001, 12051), *range(31001, 31051)]:
                self.servers.append(await asyncio.start_server(self.handle, '127.0.0.1', port))
        try:
            self.loop.run_until_complete(start())
        except Exception as exc:
            self.error = exc
        finally:
            self.ready.set()
        if not self.error:
            self.loop.run_forever()
        self.loop.close()

    def close(self):
        async def stop():
            for server in self.servers:
                server.close()
                await server.wait_closed()
            tasks = [t for t in asyncio.all_tasks() if t is not asyncio.current_task()]
            for task in tasks:
                task.cancel()
            await asyncio.gather(*tasks, return_exceptions=True)
        asyncio.run_coroutine_threadsafe(stop(), self.loop).result(5)
        self.loop.call_soon_threadsafe(self.loop.stop)
        self.thread.join(5)

    async def handle(self, reader, writer):
        try:
            header = await reader.readuntil(b'\r\n\r\n')
            lines = header.decode().split('\r\n')
            method, path, _ = lines[0].split(' ')
            headers = dict(line.lower().split(': ', 1) for line in lines[1:] if ': ' in line)
            body = await reader.readexactly(int(headers.get('content-length', 0)))
            self.requests.append((method, path, body))
            if path != '/ws':
                if path == '/init':
                    result = b'123'
                elif path == '/ps':
                    result = json.dumps({'cpu': [0, 10, 30], 'mem': [{'rss': 1000}, {'rss': 3000}, {'rss': 2000}]}).encode()
                else:
                    result = b'profile-fixture'
                writer.write(b'HTTP/1.1 200 OK\r\nContent-Length: ' + str(len(result)).encode() + b'\r\nConnection: close\r\n\r\n' + result)
                await writer.drain()
                return
            self.attempts += 1
            if self.mode == 'handshake_timeout':
                await asyncio.sleep(1)
                return
            if self.mode == 'partial' and writer.get_extra_info('sockname')[1] % 2:
                return
            if self.mode == 'retry' and self.attempts % 2:
                return
            # Values are case sensitive: re-read the original websocket key.
            key = next(line.split(': ', 1)[1] for line in lines if line.lower().startswith('sec-websocket-key:'))
            accept = base64.b64encode(hashlib.sha1((key + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest())
            if self.mode == 'bad_upgrade':
                accept = b'bad'
            response = b'HTTP/1.1 101 Switching Protocols\r\nUpgrade: WebSocket\r\nConnection: keep-alive, Upgrade\r\nSec-WebSocket-Accept: ' + accept + b'\r\n\r\n'
            # Force incremental HTTP header parsing.
            writer.write(response[:25])
            await writer.drain()
            await asyncio.sleep(0.001)
            writer.write(response[25:])
            await writer.drain()
            self.connections += 1
            while True:
                first, second = await reader.readexactly(2)
                size = second & 127
                if size == 126:
                    size, = struct.unpack('!H', await reader.readexactly(2))
                elif size == 127:
                    size, = struct.unpack('!Q', await reader.readexactly(8))
                self.masked &= bool(second & 128)
                mask = await reader.readexactly(4) if second & 128 else bytes(4)
                raw = await reader.readexactly(size)
                data = bytes(v ^ mask[i % 4] for i, v in enumerate(raw))
                if first & 15 == 10:
                    continue
                if self.mode == 'close':
                    writer.write(frame(struct.pack('!H', 1000), 8))
                    await writer.drain()
                    return
                if self.mode == 'stall':
                    continue
                if self.mode == 'corrupt':
                    data = bytes([data[0] ^ 255]) + data[1:]
                if self.mode == 'fragment':
                    middle = len(data) // 2
                    response = frame(data[:middle], fin=False) + frame(b'ping', 9) + frame(data[middle:], 0)
                    # Split headers and bodies across multiple socket reads.
                    writer.write(response[:1])
                    await writer.drain()
                    await asyncio.sleep(0.001)
                    writer.write(response[1:])
                else:
                    writer.write(frame(data))
                await writer.drain()
        except (asyncio.IncompleteReadError, ConnectionError, BrokenPipeError):
            pass
        finally:
            writer.close()
            try:
                await writer.wait_closed()
            except (ConnectionError, BrokenPipeError):
                pass


def server_process_running(name):
    listing = subprocess.run(['ps', '-A', '-o', 'comm='], text=True, capture_output=True, timeout=10)
    return any(line.strip().rsplit('/', 1)[-1] == name for line in listing.stdout.splitlines())


class ClientTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = Fixture()

    @classmethod
    def tearDownClass(cls):
        cls.server.close()

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.cwd = Path(self.directory.name)
        self.server.mode = 'echo'
        self.server.requests.clear()
        self.server.attempts = 0
        self.server.masked = True

    def tearDown(self):
        self.directory.cleanup()

    def run_client(self, *args, success=True):
        # -ps=remote: the fixture is an HTTP stand-in for a server, not a
        # gorilla.server process, so the resource samples have to come from its
        # /ps route. Local sampling has tests of its own below.
        command = [BINARY, '-f=gorilla', '-c=4', '-dc=2', '-ec=2', '-threads=2', '-en=40', '-b=128', '-check=true', '-ep=false', '-ps=remote', '-m=0', '-dt=200ms', '-dri=1ms', '-io-timeout=100ms', *args]
        result = subprocess.run(command, cwd=self.cwd, text=True, capture_output=True, timeout=20)
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def report(self, kind, prefix='', suffix=''):
        return json.loads((self.cwd / 'output/report' / f'{prefix}gorilla-{kind}{suffix}.json').read_text())

    def test_echo_payload_sizes_and_statistics(self):
        for size in [1, 125, 126, 65535, 65536]:
            with self.subTest(size=size):
                self.run_client(f'-b={size}')
                c = self.report('Connections')
                self.assertEqual((c['Total'], c['Success'], c['Failed']), (4, 4, 0))
                e = self.report('BenchEcho')
                self.assertEqual((e['Success'], e['Failed'], e['Total'], e['Payload']), (40, 0, 40, size))
                self.assertTrue(0 < e['Min'] <= e['TP50'] <= e['TP99'] <= e['Max'])
                self.assertEqual((e['CPUMin'], e['CPUAvg'], e['CPUMax']), (10, 20, 30))
                self.assertEqual((e['MEMMin'], e['MEMAvg'], e['MEMMax']), (2000, 2500, 3000))
                self.assertAlmostEqual(e['EER'], e['TPS'] / 20)
                self.assertAlmostEqual(e['MEMEER'], e['TPS'] / (2500 / (1024 * 1024)))
                self.assertTrue(self.server.masked)

    def test_fragmented_messages_and_ping(self):
        self.server.mode = 'fragment'
        self.run_client('-b=1024')
        self.assertEqual(self.report('BenchEcho')['Success'], 40)

    def test_corruption_is_counted(self):
        self.server.mode = 'corrupt'
        self.run_client(success=False)
        self.assertEqual(self.report('BenchEcho')['Failed'], 40)
        self.run_client('-check=false')
        self.assertEqual(self.report('BenchEcho')['Success'], 40)

    def test_disconnect_and_response_timeout(self):
        for mode in ['close', 'stall']:
            with self.subTest(mode=mode):
                self.server.mode = mode
                self.run_client(success=False)
                self.assertEqual(self.report('BenchEcho')['Failed'], 40)

    def test_upgrade_rejection_and_timeout(self):
        for mode in ['bad_upgrade', 'handshake_timeout']:
            with self.subTest(mode=mode):
                self.server.mode = mode
                self.run_client('-dr=2', '-dt=20ms', success=False)
                self.assertEqual(self.report('Connections')['Failed'], 4)

    def test_partial_connections(self):
        self.server.mode = 'partial'
        self.run_client('-dr=1', success=False)
        self.assertEqual(self.report('Connections')['Success'], 2)
        self.assertEqual(self.report('BenchEcho')['Success'], 40)

    def test_rate_covers_all_connections(self):
        self.run_client('-rate=true', '-c=8', '-rc=1', '-rd=1', '-rr=20', '-rbs=1')
        r = self.report('BenchPipeline')
        self.assertTrue(120 <= r['RecvTimes'] <= r['SendTimes'] <= 160, r)
        self.assertEqual(r['RecvTimes'], r['SendTimes'], r)
        self.assertEqual(r['Concurrency'], 1, r)
        # -rd=1: a second's packets are its TPS.
        self.assertEqual(r['TPS'], r['RecvTimes'], r)
        # -rbs=1 holds no whole frame, so each write carries one.
        self.assertEqual(r['Pipeline'], 1, r)

    # -rpl sets the messages each write carries, lowered to divide -rr, and
    # the report records what ran, which the Summary shows as Rate Pipeline.
    def test_rate_pipeline(self):
        # 0: -rbs holds 120 of these 128-byte frames, capped at -rr.
        for pipeline, want in [('4', 4), ('7', 5), ('0', 20)]:
            self.run_client('-rate=true', '-rd=1', '-rr=20', f'-rpl={pipeline}', '-rc=1')
            r = self.report('BenchPipeline')
            self.assertEqual(r['Pipeline'], want, r)
            self.assertEqual(r['RecvTimes'], r['SendTimes'], r)
        self.run_client('-r=true')
        summary = (self.cwd / 'output/report/Summary.md').read_text()
        self.assertIn('| Rate Pipeline    | 20 ', summary)
        self.run_client('-rpl=oops', success=False)

    # A rate report written before TPS was recorded gets it from Packet Recv
    # and Duration when the report is read again, and ranks by it.
    def test_rate_tps_of_an_earlier_report(self):
        directory = self.cwd / 'output/report'
        directory.mkdir(parents=True)
        for framework, packets in [('fasthttp', 16587970), ('gorilla', 39809399)]:
            (directory / f'{framework}-BenchPipeline.json').write_text(json.dumps(
                {'Framework': framework, 'BenchClient': 'benchcli-uwscpp', 'Duration': 10_000_000_000,
                 'RecvTimes': packets, 'EchoEER': 1}))
        self.run_client('-r=true')
        lines = (directory / 'BenchPipeline.md').read_text(encoding='utf-8').splitlines()
        self.assertEqual([cell.strip() for cell in lines[0].split('|')[1:6]],
                         ['Framework', 'Lang', 'TPS [↓1]', 'CPU EER [↓2]', 'MEM EER [↓3]'])
        self.assertIn('| 3980939 100% |', lines[2])
        self.assertIn('| 1658797  41% |', lines[3])

    # Every report carries the language its framework's server is written in,
    # right after Framework: in the JSON a run writes, and, for a report written
    # before the column existed, from config.Langs when it is read again.
    def test_lang_column(self):
        self.run_client()
        self.assertEqual(self.report('Connections')['Lang'], 'go')
        self.assertEqual(self.report('BenchEcho')['Lang'], 'go')
        directory = self.cwd / 'output/report'
        for framework in ['tokio_tungstenite', 'uwebsockets']:
            (directory / f'{framework}-BenchEcho.json').write_text(json.dumps(
                {'Framework': framework, 'BenchClient': 'benchcli-uwscpp', 'TPS': 1}))
        self.run_client('-r=true', '-sort=framework')
        lines = (directory / 'BenchEcho.md').read_text(encoding='utf-8').splitlines()
        self.assertEqual([cell.strip() for cell in lines[0].split('|')[1:3]], ['Framework', 'Lang'])
        langs = {cells[1]: cells[2] for cells in
                 ([cell.strip() for cell in line.split('|')] for line in lines[2:])}
        self.assertEqual(langs, {'gorilla': 'go', 'tokio_tungstenite': 'rust', 'uwebsockets': 'c++'})

    def test_retry(self):
        self.server.mode = 'retry'
        self.run_client('-c=1', '-dc=1', '-dr=2')
        self.assertEqual(self.report('Connections')['Success'], 1)
        self.assertEqual(self.server.attempts, 2)

    def test_rate_and_global_limit(self):
        for batch_size in [1, 16384]:
            self.run_client('-rate=true', '-rd=1', '-rr=100', '-rl=40', f'-rbs={batch_size}', '-rc=1')
            r = self.report('BenchPipeline')
            self.assertTrue(0 < r['RecvTimes'] <= r['SendTimes'] <= 80, r)
            self.assertEqual(r['RecvTimes'], r['SendTimes'], r)
            self.assertEqual(r['SendBytes'], r['SendTimes'] * 128)
            self.assertEqual(r['RecvBytes'], r['RecvTimes'] * 128)
            self.assertEqual(r['Duration'], 1000000000)

    def test_echo_limit(self):
        self.run_client('-el=20', '-en=40', '-io-timeout=1s')
        self.assertGreaterEqual(self.report('BenchEcho')['Used'], 900000000)

    def test_reports_profiles_and_flags(self):
        self.run_client('-preffix=test_', '-suffix=_small', '-tpn=false', '-ep=true', '-epd=1', '-rate=true', '-rd=1', '-rp=true', '-rpd=1', '-pi=42')
        self.assertEqual(self.report('BenchEcho', 'test_', '_small')['TP99'], 0)
        for kind in ['BenchEcho', 'BenchPipeline']:
            for ext in ['cpu', 'mem']:
                path = self.cwd / f'output/report/test_gorilla-{kind}_small.pprof.{ext}'
                self.assertEqual(path.read_bytes(), b'profile-fixture')
        init = [json.loads(body) for method, path, body in self.server.requests if path == '/init']
        self.assertEqual(init, [{'PsInterval': 42000000}])
        self.assertEqual(self.report('BenchEcho', 'test_', '_small')['EchoPprof'], 'on')
        self.assertEqual(self.report('BenchPipeline', 'test_', '_small')['RatePprof'], 'on')
        self.run_client('-r=true', '-preffix=test_', '-suffix=_small', '-tpn=false')
        for kind in ['Connections', 'BenchEcho', 'BenchPipeline']:
            md = (self.cwd / f'output/report/test_{kind}_small.md').read_text()
            self.assertIn('gorilla', md)
            self.assertNotIn('TP99', md)
        summary = (self.cwd / 'output/report/test_Summary_small.md').read_text()
        rows = {cells[1]: cells[2] for cells in
                ([cell.strip() for cell in line.split('|')] for line in summary.splitlines()[2:])}
        self.assertEqual((rows['Echo Pprof'], rows['Rate Pprof']), ('on', 'on'), summary)
        # Both are off unless asked for.
        self.run_client('-rate=true', '-rd=1')
        self.assertEqual((self.report('BenchEcho')['EchoPprof'], self.report('BenchPipeline')['RatePprof']), ('off', 'off'))

    # Only a Go server has /debug/pprof, so a profile is not asked of any other one - not even
    # with -ep and -rp on - and there is no pprof hint to print for it either.
    def test_no_profile_for_a_server_without_pprof(self):
        result = self.run_client('-f=uwebsockets', '-ep=true', '-epd=1', '-rate=true', '-rd=1', '-rp=true', '-rpd=1')
        self.assertEqual([path for _, path, _ in self.server.requests if 'pprof' in path], [])
        directory = self.cwd / 'output/report'
        self.assertTrue((directory / 'uwebsockets-BenchEcho.json').exists())
        self.assertEqual(list(directory.glob('*.pprof.*')), [])
        self.assertNotIn('pprof', result.stdout + result.stderr)

    # The Pool comes from /taskpool only for a framework that takes a pool: gorilla takes none,
    # so it is not asked and reads "-", while uwebsockets is asked and reads what it answered.
    def test_taskpool_asked_only_of_pooled_frameworks(self):
        for framework, pool, asked in [('gorilla', '-', False), ('uwebsockets', 'profile-fixture', True)]:
            self.server.requests.clear()
            self.run_client(f'-f={framework}')
            self.assertEqual(any(path == '/taskpool' for _, path, _ in self.server.requests), asked, framework)
            report = json.loads((self.cwd / 'output/report' / f'{framework}-BenchEcho.json').read_text())
            self.assertEqual(report['TaskPool'], pool, framework)

    # A single-node run samples the server process itself. With no such process
    # here - which is also what a server on the other node of a two-node run
    # looks like - the client has to say so and go back to asking the server.
    def test_local_sampling_falls_back_without_a_server_process(self):
        if server_process_running('gorilla.server'):
            self.skipTest('a gorilla.server is running here, so local sampling would find it')
        result = self.run_client('-ps=local')
        self.assertIn('cannot sample the server from this machine', result.stderr)
        e = self.report('BenchEcho')
        self.assertEqual((e['CPUMin'], e['CPUAvg'], e['CPUMax']), (10, 20, 30))
        init = [path for method, path, body in self.server.requests if path == '/init']
        self.assertEqual(init, ['/init'])

    # script/report.sh passes -sort on every run, and the row order has to be
    # the Go client's: the two write the same tables, and a report is diffed
    # against the one before it.
    def test_report_row_order(self):
        directory = self.cwd / 'output/report'
        directory.mkdir(parents=True)

        # BenchPipeline ranks by TPS, then EchoEER, then MEMEER; RecvBytes runs
        # the other way here, so a table ranked by it would come out reversed.
        def write(scores, eer=None, memeer=None):
            for framework, score in scores.items():
                for kind in ['Connections', 'BenchEcho', 'BenchPipeline']:
                    (directory / f'{framework}-{kind}.json').write_text(json.dumps(
                        {'Framework': framework, 'BenchClient': 'benchcli-uwscpp',
                         'TPS': score, 'RecvTimes': score * 10, 'RecvBytes': 100 - score,
                         'EER': (eer or {}).get(framework, 0),
                         'EchoEER': (eer or {}).get(framework, 0),
                         'MEMEER': (memeer or {}).get(framework, 0)}))

        # Column 1 is Framework: the report's columns are its struct's fields in
        # benchcli-go/report, and Framework is the first of them.
        def order(*args):
            self.run_client('-r=true', *args)
            return [[line.split('|')[1].strip()
                     for line in (directory / f'{kind}.md').read_text().splitlines()[2:]]
                    for kind in ['Connections', 'BenchEcho', 'BenchPipeline']]

        # fasthttp comes first by name, gorilla by result.
        write({'fasthttp': 10, 'gorilla': 20})
        self.assertEqual(order(), [['gorilla', 'fasthttp']] * 3)
        self.assertEqual(order('-sort=result'), [['gorilla', 'fasthttp']] * 3)
        self.assertEqual(order('-sort=framework'), [['fasthttp', 'gorilla']] * 3)

        # A tie keeps the framework order, so a run is reproducible rather than
        # merely sorted - including a benchmark that did not run at all.
        write({'fasthttp': 0, 'gorilla': 0})
        self.assertEqual(order('-sort=result'), [['fasthttp', 'gorilla']] * 3)

        # BenchEcho and BenchPipeline break a tie by CPU EER; Connections, which has
        # none, keeps the framework order.
        write({'fasthttp': 7, 'gorilla': 7}, eer={'fasthttp': 1, 'gorilla': 2})
        self.assertEqual(order('-sort=result'),
                         [['fasthttp', 'gorilla'], ['gorilla', 'fasthttp'], ['gorilla', 'fasthttp']])

        # And a tie on both by MEM EER.
        write({'fasthttp': 7, 'gorilla': 7}, eer={'fasthttp': 2, 'gorilla': 2}, memeer={'fasthttp': 1, 'gorilla': 3})
        self.assertEqual(order('-sort=result'),
                         [['fasthttp', 'gorilla'], ['gorilla', 'fasthttp'], ['gorilla', 'fasthttp']])

        # The ranked column carries each row's share of the best, right-aligned
        # and in either order, as benchcli-go writes it.
        write({'fasthttp': 10, 'gorilla': 40})
        for arg in ['-sort=result', '-sort=framework']:
            self.run_client('-r=true', arg)
            for kind in ['Connections', 'BenchEcho', 'BenchPipeline']:
                md = (directory / f'{kind}.md').read_text()
                self.assertIn(' 40 100% ', md)
                self.assertIn(' 10  25% ', md)

        # CPU EER and MEM EER carry percentages of their own best, in BenchEcho
        # and BenchPipeline. Their titles are wider than these cells, so the
        # cells are centred under them rather than filling them.
        write({'fasthttp': 10, 'gorilla': 40}, eer={'fasthttp': 250.5, 'gorilla': 62.5},
              memeer={'fasthttp': 3, 'gorilla': 12})
        self.run_client('-r=true')
        for kind in ['BenchEcho', 'BenchPipeline']:
            md = (directory / f'{kind}.md').read_text()
            self.assertIn(' 250.50 100% ', md)
            self.assertIn('  62.50  24% ', md)
            self.assertIn('  3.00  25% ', md)
            self.assertIn(' 12.00 100% ', md)
        self.assertEqual((directory / 'Connections.md').read_text().count('%'), 2)

        # One of the EERs whose best*100/best floored to 99, which left its
        # table without a 100% row.
        write({'fasthttp': 10, 'gorilla': 40}, eer={'fasthttp': 697.86464300696265, 'gorilla': 1395.7292860139253})
        self.run_client('-r=true')
        for kind in ['BenchEcho', 'BenchPipeline']:
            md = (directory / f'{kind}.md').read_text()
            self.assertIn('| 1395.73 100% |', md)
            self.assertIn('|  697.86  50% |', md)

        # The rank columns' titles carry [↓1], [↓2] and [↓3] in either order, and the
        # table stays in line: every line is as many characters wide.
        for arg in ['-sort=result', '-sort=framework']:
            self.run_client('-r=true', arg)
            eer = [' TPS [↓1] ', ' CPU EER [↓2] ', ' MEM EER [↓3] ']
            for kind, markers in [('Connections', [' TPS [↓1] ']), ('BenchEcho', eer), ('BenchPipeline', eer)]:
                lines = (directory / f'{kind}.md').read_text(encoding='utf-8').splitlines()
                for marker in markers:
                    self.assertIn(marker, lines[0])
                self.assertEqual(lines[0].count('↓'), len(markers))
                self.assertEqual({len(line) for line in lines}, {len(lines[0])}, lines)

    # The run's parameters leave the three tables for a Summary table in front of
    # them, in its own file and in the console, as benchcli-go writes it.
    def test_summary(self):
        directory = self.cwd / 'output/report'
        directory.mkdir(parents=True)
        for framework, pool in [('fasthttp', '-'), ('gorilla', 'nbio')]:
            common = {'Framework': framework, 'BenchClient': 'benchcli-uwscpp', 'TaskPool': pool}
            (directory / f'{framework}-Connections.json').write_text(json.dumps(
                {**common, 'TPS': 1, 'Concurrency': 20}))
            (directory / f'{framework}-BenchEcho.json').write_text(json.dumps(
                {**common, 'TPS': 1, 'Conns': 100, 'Concurrency': 50, 'Total': 1000, 'Payload': 64}))
        result = self.run_client('-r=true')
        summary = (directory / 'Summary.md').read_text()
        lines = summary.splitlines()
        self.assertEqual([cell.strip() for cell in lines[0].split('|')[1:4]], ['Parameter', 'Value', 'Description'])
        # Left-aligned: every cell a space, its text, then only spaces.
        for line in lines:
            for cell in line.strip('|').split('|'):
                self.assertTrue(cell.startswith(' ') and not cell.startswith('  '), (cell, summary))
        rows = [[cell.strip() for cell in line.split('|')[1:3]] for line in lines[2:]]
        self.assertEqual(rows, [['Project', 'GO-WEBSOCKET-BENCHMARK'], ['Client', 'cpp-uwebsockets'], ['Pool', 'nbio'],
                                ['Conns', '100'], ['Payload', '64'], ['Dial Concurrency', '20'],
                                ['Echo Concurrency', '50'], ['Echo Total', '1000']])
        self.assertEqual(lines[2].split('|')[3].strip(), 'what this run benchmarks (-project)')
        self.assertEqual(lines[4].split('|')[3].strip(), 'task pool, used by Go event-loop frameworks only')
        for kind in ['Connections', 'BenchEcho']:
            title = (directory / f'{kind}.md').read_text().splitlines()[0]
            for column in ['Client', 'Pool', 'Conns', 'Concurrency', 'Payload']:
                self.assertNotIn(f' {column} ', title)
        rule = '-' * 100
        self.assertIn(f'{rule}\n[Summary]\n\n{summary}\n{rule}\n[Connections]\n\n', result.stdout)
        self.assertIn(f'{rule}\n[BenchPipeline]\n\n(no results)\n\n{rule}\n', result.stdout)
        # -project names the row, spaces and all, and an empty one leaves it out.
        self.run_client('-r=true', '-project=uwebsockets threads, 3 CPUs')
        first = (directory / 'Summary.md').read_text().splitlines()[2]
        self.assertEqual([cell.strip() for cell in first.split('|')[1:3]], ['Project', 'uwebsockets threads, 3 CPUs'])
        self.run_client('-r=true', '-project=')
        self.assertNotIn('Project', (directory / 'Summary.md').read_text())

    def test_invalid_arguments_and_empty_echo(self):
        for arg in ['-f=invalid', '-c=-1', '-dt=oops', '-check=oops', '-unknown=1', '-suffix=../x', '-ps=oops', '-sort=oops']:
            self.run_client(arg, success=False)
        self.run_client('-en=0')
        self.assertEqual(self.report('BenchEcho')['Total'], 0)


if __name__ == '__main__':
    unittest.main(verbosity=2)
