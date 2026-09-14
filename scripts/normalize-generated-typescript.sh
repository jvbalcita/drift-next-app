#!/usr/bin/env bash

set -euo pipefail

output_root=${1:-apps/console/src/gen}

# protoc-gen-es v2 emits an extra terminal blank line. Normalize the generated
# output so it obeys the repository's whitespace gate without changing any
# generated declarations.
find "$output_root" -type f -name '*.ts' -exec perl -0pi -e 's/(?:\r?\n)+\z/\n/' {} +
