#!/usr/bin/env python3
"""Keep the native clients' ports and report schema aligned with Go.

Writes a C++ header for benchcli-uwscpp, or plain JSON for benchcli-rust when
the output path ends in .json."""
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
# The Lang column: config.Langs maps each framework to one of the Lang* constants.
langs_block = re.search(r'var Langs = map\[string\]string\{(.*?)\n\}', config, re.S).group(1)
langs = {names[name]: names[lang] for name, lang in re.findall(r'^\s*(\w+):\s*(\w+),', langs_block, re.M)}
missing = [f for f in frameworks if f not in langs]
if missing:
    raise SystemExit(f'No language in config.Langs for: {", ".join(missing)}')
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
        # md:"-" keeps a field in the JSON and out of the tables and the console.
        fields.append(dict(key=tags['json'], title=tags['md'], fmt=tags.get('fmt', ''),
                           optional='tpn' in tags, hidden=tags['md'] == '-',
                           # rank:"N" is the Nth key -sort=result compares, and every
                           # rank column carries each row's percentage of the best.
                           rank=int(tags.get('rank', 0)),
                           # summary:"<name>" moves the field out of the table into the
                           # Summary table, under that name.
                           summary=tags.get('summary', ''),
                           string=typ == 'string', floating=typ.startswith('float')))
    if not fields:
        raise SystemExit(f'No report fields found for {kind}')
    schemas[kind] = fields
assert len(ports) == len(frameworks) and ports
summary_go = (root / 'benchcli-go/report/summary.go').read_text()
summary_block = re.search(r'var SummaryParameters = \[\]SummaryParameter\{\n(.*?)\n\}', summary_go, re.S).group(1)
summary_order = [dict(name=name, description=description) for name, description in
                 re.findall(r'\{"([^"]+)",\s*"([^"]*)"\}', summary_block)]
if not summary_order:
    raise SystemExit('No SummaryParameters found in summary.go')
metadata = dict(ports=ports, frameworks=frameworks, langs=langs, schemas=schemas, summaryOrder=summary_order)
path = pathlib.Path(sys.argv[1])
path.parent.mkdir(parents=True, exist_ok=True)
if path.suffix == '.json':
    path.write_text(json.dumps(metadata) + '\n')
else:
    path.write_text('// Generated from config/ and benchcli-go/report/. Do not edit.\n'
                    'inline const auto metadata = json::parse(R"META(' + json.dumps(metadata) + ')META");\n')
