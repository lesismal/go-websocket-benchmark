#!/bin/bash

# Row order for the three report tables. BENCH_REPORT_SORT is set and
# documented in script/config.sh: "result" (the default) ranks the best result
# first, "framework" keeps config.FrameworkList's order.
#
# It is passed before "$@" on purpose, so that a -sort given on the command
# line still wins: the client takes the last value of a repeated flag. Running
# this script on its own leaves the variable unset and the flag off, which
# leaves the client its own default rather than a second copy of it here.
#
# Nothing else in a report depends on this - both orders carry the same rows
# and the same numbers - so re-running just this script is enough to read a
# finished benchmark the other way round:
#
#   bash script/report.sh -sort=framework
report_sort_flag=""
if [ -n "${BENCH_REPORT_SORT:-}" ]; then
    report_sort_flag="-sort=${BENCH_REPORT_SORT}"
fi
# The Summary's Project row, from script/config.sh's BENCH_PROJECT, and in the
# same place for the same reason: a -project on the command line still wins.
# An array, since a project name may have spaces in it; set but empty passes
# -project= through, which leaves the row out.
report_project_flag=()
if [ -n "${BENCH_PROJECT+set}" ]; then
    report_project_flag=("-project=${BENCH_PROJECT}")
fi

echo "generate report ..."
echo
./output/bin/bench.client -r=true ${report_sort_flag} "${report_project_flag[@]}" "$@"
echo
echo "generate report done"
