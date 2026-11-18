#!/usr/bin/env bash
# Live terminal preview for CLI output design.
# Usage: tools/preview/watch.sh [scenario ...]
# Scenarios: overview router mixed lab apple notices plain all (default: all)
# Re-renders whenever a .go file in this folder changes. Uses inotifywait on
# Linux; falls back to polling (macOS, or when inotify-tools is missing).
cd "$(dirname "$0")" || exit 1

render() {
	clear
	go run . "$@" 2>&1
	printf '\n\033[2m(watching *.go in tools/preview — ctrl-c to stop)\033[0m\n'
}

stamp() { cat ./*.go | cksum; }

render "$@"
if command -v inotifywait >/dev/null 2>&1; then
	while inotifywait -qq -e close_write,moved_to --include '\.go$' .; do
		render "$@"
	done
else
	last=$(stamp)
	while sleep 0.5; do
		now=$(stamp)
		if [ "$now" != "$last" ]; then
			last=$now
			render "$@"
		fi
	done
fi
