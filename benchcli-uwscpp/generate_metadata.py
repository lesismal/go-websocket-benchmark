#!/usr/bin/env python3
"""Keep the standalone C++ client's ports and report schema aligned with Go."""
import json
import pathlib
import re
import sys

root = pathlib.Path(__file__).resolve().parent.parent
config = (root / 'config/config.go').read_text()
names = dict(re.findall(r'^\s*(\w+)\s*=\s*"([^"\n]+)"', config, re.M))
ports = {names[name]: [int(lo), int(hi)] for name, lo, hi in
         re.findall(r'^\s*(\w+):\s*"(\d+):(\d+)"', config, re.M)}
order = re.search(r'var FrameworkList = \[\]string\{(.*?)\}', config, re.S).group(1)
frameworks = [names[name] for name in re.findall(r'(\w+)\s*,', order)]
schemas = {}
for kind, filename in [('Connections', 'connections_report.go'),
                       ('BenchEcho', 'benchecho_report.go'),
                       ('BenchRate', 'benchrate_report.go')]:
    fields = []
    for line in (root / 'benchcli-go/report' / filename).read_text().splitlines():
        if line.lstrip().startswith('//'):
            continue
        match = re.search(r'^\s*\w+\s+(\w+)\s+`([^`]+)`', line)
        if not match:
            continue
        typ, tags = match.groups()
        tags = dict(re.findall(r'(\w+):"([^"]*)"', tags))
        if tags.get('json', '-') == '-':
            continue
        fields.append(dict(key=tags['json'], title=tags['md'], fmt=tags.get('fmt', ''),
                           optional='tpn' in tags, string=typ == 'string', floating=typ.startswith('float')))
    if not fields:
        raise SystemExit(f'No report fields found for {kind}')
    schemas[kind] = fields
assert len(ports) == len(frameworks) and ports
metadata = dict(ports=ports, frameworks=frameworks, schemas=schemas)
path = pathlib.Path(sys.argv[1])
path.parent.mkdir(parents=True, exist_ok=True)
path.write_text('// Generated from config/ and benchcli-go/report/. Do not edit.\n'
                'inline const auto metadata = json::parse(R"META(' + json.dumps(metadata) + ')META");\n')
