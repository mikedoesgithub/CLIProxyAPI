#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
balance_dir="${1:-$PWD/.balance}"
mkdir -p -- "$balance_dir"
balance_dir="$(cd -- "$balance_dir" && pwd)"
python3 -m venv "$balance_dir/venv"
"$balance_dir/venv/bin/python" -m pip install --requirement "$script_dir/requirements.txt"
"$balance_dir/venv/bin/python" "$script_dir/download_model.py" "$balance_dir/model"
printf '\nRun the local classifier:\n  %q %q --model-dir %q --threads 2\n' "$balance_dir/venv/bin/python" "$script_dir/server.py" "$balance_dir/model"
printf 'Enable routing.balance using tools/balance/policy.example.yaml after matching your actual model aliases.\n'
