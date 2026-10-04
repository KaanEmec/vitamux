"""Entry point: python -m src.main. Environment: see README.md."""

import logging
import os
import sys
from pathlib import Path

from . import garmin, replay, server


def main():
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    logging.getLogger("garminconnect").setLevel(logging.CRITICAL)  # its logs can carry URLs and bodies
    os.environ.pop("GARMINTOKENS", None)  # never load tokens from a file
    try:
        secret = Path(os.environ["VITAMUX_SIDECAR_SECRET_FILE"]).read_text().strip()
    except (KeyError, OSError) as e:
        sys.exit(f"VITAMUX_SIDECAR_SECRET_FILE is required and must be readable ({type(e).__name__})")
    if not secret:
        sys.exit("VITAMUX_SIDECAR_SECRET_FILE is empty")
    if os.environ.get("REPLAY") == "1":  # answer from testdata/replay.json, never call Garmin
        replay.install(garmin, Path("testdata/replay.json"))
    srv = server.make_server(os.environ.get("SIDECAR_ADDR", "0.0.0.0:8080"), secret)
    logging.info("listening on %s:%s", *srv.server_address[:2])
    srv.serve_forever()


if __name__ == "__main__":
    main()
