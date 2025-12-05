#!/usr/bin/env python3
"""
M4.10: Helper library for FD 3 Return protocol
Provides convenient functions for Python agents to return JSON results
"""

import os
import json
import sys


def holt_return(data):
    """
    Write result JSON to FD 3.

    Args:
        data: Dictionary containing artefact_type, artefact_payload, summary, etc.

    Example:
        holt_return({
            "artefact_type": "CodeCommit",
            "artefact_payload": "abc123",
            "summary": "Work complete"
        })
    """
    try:
        # Open FD 3 for writing
        fd3 = os.fdopen(3, 'w')
        json.dump(data, fd3, indent=2)
        fd3.write('\n')
        fd3.close()
    except OSError as e:
        # FD 3 not available (running outside pup) - fallback to stdout
        print(f"Warning: FD 3 not available, falling back to stdout: {e}", file=sys.stderr)
        print(json.dumps(data, indent=2))
    except Exception as e:
        print(f"ERROR: Failed to write to FD 3: {e}", file=sys.stderr)
        raise


# Example usage (commented out):
# if __name__ == "__main__":
#     holt_return({
#         "artefact_type": "TestResult",
#         "artefact_payload": "all tests passed",
#         "summary": "Test execution complete"
#     })
