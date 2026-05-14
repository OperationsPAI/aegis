#!/bin/bash
set -e
set -u
set -o pipefail

# if [ -z "$(ls -A /app/algorithms)" ]; then
#     echo "Algorithms directory is empty, copying legacy algorithms...";
#     # Iterate through each file in the legacy_algorithms directory and only copy if it doesn't exist
#     for file in /app/legacy_algorithms/*; do
#         filename=$(basename "$file")
#         if [ ! -e "/app/algorithms/$filename" ]; then
#             cp -r "$file" "/app/algorithms/"
#             echo "Copied: $filename"
#         else
#             echo "File exists, skipping: $filename"
#         fi
#     done
#     echo "Legacy algorithms copied successfully.";
# else
#     echo "Algorithms directory is not empty, checking for missing files...";
#     # Check for files that need to be copied even if the directory is not empty
#     for file in /app/legacy_algorithms/*; do
#         filename=$(basename "$file")
#         if [ ! -e "/app/algorithms/$filename" ]; then
#             cp -r "$file" "/app/algorithms/"
#             echo "Copied missing file: $filename"
#         fi
#     done
# fi

# Dispatch: route to the correct binary based on the first argument.
case "${1:-}" in
    sso)
        shift
        echo "Executing /app/sso with command: $@"
        exec /app/sso "$@"
        ;;
    aegis-notify)
        shift
        echo "Executing /app/aegis-notify with command: $@"
        exec /app/aegis-notify "$@"
        ;;
    aegis-blob)
        shift
        echo "Executing /app/aegis-blob with command: $@"
        exec /app/aegis-blob "$@"
        ;;
    aegis-gateway)
        shift
        echo "Executing /app/aegis-gateway with command: $@"
        exec /app/aegis-gateway "$@"
        ;;
    aegis-configcenter)
        shift
        echo "Executing /app/aegis-configcenter with command: $@"
        exec /app/aegis-configcenter "$@"
        ;;
esac

echo "Executing /app/exp with command: $@"
exec /app/exp "$@"