#!/bin/bash

# Simple set of tests for arocurl wrapper running in dry run mode (no network
# communication is happening during the run).

# In case of any failure, the script will immediatelly exit with error.
set -o errexit

# Temporary file to capture commant output
TMP_FILE=$(mktemp)

#
# Test cases
#

echo "Test: Create Request"
./arocurl.sh -d -c \
  PUT "/subscriptions/9616cdbb-45b7-4359-9487-8e1a2ca3962c?api-version=2.0" \
  --json "{\"state\":\"Registered\", \"registrationDate\": \"2025-05-25\"}" \
  > "${TMP_FILE}"
OUT=$(grep ^curl "${TMP_FILE}")
EXP='curl -H @- --request PUT localhost:8443/subscriptions/9616cdbb-45b7-4359-9487-8e1a2ca3962c?api-version=2.0 --json {"state":"Registered", "registrationDate": "2025-05-25"}'
diff <(echo $EXP) <(echo $OUT)
OUT=$(grep ^X-Ms-Arm-Resource-System-Data "${TMP_FILE}")
EXP='X-Ms-Arm-Resource-System-Data: {"createdBy": "shadowman@example.com", "createdByType": "user", "createdAt": "2020-10-20T20:10:20+00:00"}'
diff <(echo $EXP) <(echo $OUT)

echo "Test: Get Request"
./arocurl.sh -d \
  GET "/subscriptions/9616cdbb-45b7-4359-9487-8e1a2ca3962c?api-version=2.0" \
  > "${TMP_FILE}"
if grep ^X-Ms-Arm-Resource-System-Data "${TMP_FILE}"; then
  echo "This header should not be send for the request"
  exit 1
fi

echo "Test: Passing Headers"
./arocurl.sh -d -H "X-Foo-Bar-Data: BAZ" -H "X-Foo-Bar-Code: 001" \
  GET "/subscriptions/9616cdbb-45b7-4359-9487-8e1a2ca3962c?api-version=2.0" \
  > "${TMP_FILE}"
OUT=$(grep ^X-Foo-Bar-Data "${TMP_FILE}")
EXP='X-Foo-Bar-Data: BAZ'
diff <(echo $EXP) <(echo $OUT)
OUT=$(grep ^X-Foo-Bar-Code "${TMP_FILE}")
EXP='X-Foo-Bar-Code: 001'
diff <(echo $EXP) <(echo $OUT)

#
# Teardown
#

rm "${TMP_FILE}"
