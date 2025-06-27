#!/usr/bin/env bash

# source: https://github.com/openshift/origin/blob/4f6158d472124310f439220901987a14f06cfee0/hack/lib/util/misc.sh#L121-L129
# os::util::sed attempts to make our Bash scripts agnostic to the platform
# on which they run `sed` by glossing over a discrepancy in flag use in GNU.
#
# Globals:
#  None
# Arguments:
#  - all: arguments to pass to `sed -i`
# Return:
#  None
function os::util::sed() {
	local sudo="${USE_SUDO:+sudo}"
	if LANG=C sed --help 2>&1 | grep -q "GNU sed"; then
		${sudo} sed -i'' "$@"
	else
		${sudo} sed -i '' "$@"
	fi
}
readonly -f os::util::sed
