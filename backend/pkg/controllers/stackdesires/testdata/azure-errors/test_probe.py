#!/usr/bin/env python3
"""Offline credential-redaction regression tests; never invoke Azure CLI."""

import contextlib
import io
import subprocess
import traceback
import unittest
from unittest.mock import patch

from probe import run_cli


class CLIFailureRedactionTest(unittest.TestCase):
    def test_subprocess_failures_do_not_disclose_credentials(self):
        secret = "synthetic-secret-must-not-appear"
        arguments = ["login", "--service-principal", "--username", "TEST_CLIENT_ID",
                     "--password", secret, "--tenant", "TEST_TENANT_ID"]
        command = ["az", *arguments, "--output", "json"]
        failures = [
            subprocess.TimeoutExpired(command, 180, output=secret, stderr=secret),
            OSError("launch failed: " + repr(command)),
            subprocess.CalledProcessError(1, command, output=secret, stderr=secret),
            UnicodeDecodeError("utf-8", secret.encode(), 0, 1, secret),
        ]
        for failure in failures:
            with self.subTest(exception=type(failure).__name__):
                stdout, stderr = io.StringIO(), io.StringIO()
                with patch("probe.subprocess.run", side_effect=failure) as run:
                    with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                        try:
                            run_cli(arguments, {})
                        except RuntimeError as error:
                            self.assertTrue(error.__suppress_context__)
                            traceback.print_exc()
                        else:
                            self.fail("Expected sanitized invocation failure")
                run.assert_called_once_with(command, env={}, capture_output=True, text=True, timeout=180)
                rendered = stdout.getvalue() + stderr.getvalue()
                self.assertIn("RuntimeError: Azure CLI invocation failed; command and output withheld", rendered)
                for forbidden in (secret, repr(command), "--password", "TEST_CLIENT_ID",
                                  "TEST_TENANT_ID", type(failure).__name__):
                    self.assertNotIn(forbidden, rendered)


if __name__ == "__main__":
    unittest.main()
