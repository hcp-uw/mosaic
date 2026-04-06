#!/usr/bin/env python3
"""
Generates badge_synced.pdf (green) and badge_remote.pdf (grey) directly into
their imageset folders.  No third-party libraries required.

Run once from this directory:
    python3 generate_badges.py
"""

import os
import struct

# Bezier constant for circle approximation
K = 0.5523

def circle_path(cx, cy, r):
    """Return PDF path operators for a circle at (cx,cy) with radius r."""
    k = K * r
    return (
        f"{cx+r:.4f} {cy:.4f} m\n"
        f"{cx+r:.4f} {cy+k:.4f} {cx+k:.4f} {cy+r:.4f} {cx:.4f} {cy+r:.4f} c\n"
        f"{cx-k:.4f} {cy+r:.4f} {cx-r:.4f} {cy+k:.4f} {cx-r:.4f} {cy:.4f} c\n"
        f"{cx-r:.4f} {cy-k:.4f} {cx-k:.4f} {cy-r:.4f} {cx:.4f} {cy-r:.4f} c\n"
        f"{cx+k:.4f} {cy-r:.4f} {cx+r:.4f} {cy-k:.4f} {cx+r:.4f} {cy:.4f} c\n"
        f"f\n"
    )

def make_badge_pdf(r, g, b):
    """
    Build a minimal single-page PDF (18×18 pt) with a filled circle in (r,g,b).
    Returns bytes.
    """
    # Content stream: set fill color, draw circle
    stream_body = (
        f"{r:.4f} {g:.4f} {b:.4f} rg\n"
        + circle_path(9, 9, 7.5)
    ).encode()

    length = len(stream_body)

    body = (
        b"%PDF-1.4\n"
        b"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n\n"
        b"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n\n"
        b"3 0 obj\n"
        b"<< /Type /Page /Parent 2 0 R\n"
        b"   /MediaBox [0 0 18 18]\n"
        b"   /Contents 4 0 R\n"
        b"   /Resources << /ProcSet [/PDF] >> >>\n"
        b"endobj\n\n"
        + f"4 0 obj\n<< /Length {length} >>\nstream\n".encode()
        + stream_body
        + b"\nendstream\nendobj\n\n"
    )

    # Cross-reference table
    xref_pos = len(body)
    offsets = []
    pos = 0
    for chunk in [
        b"%PDF-1.4\n",
        b"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n\n",
        b"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n\n",
    ]:
        offsets.append(pos)
        pos += len(chunk)

    # Recalculate offsets properly by scanning the body bytes
    offsets = []
    search = body
    for marker in [b"1 0 obj", b"2 0 obj", b"3 0 obj", b"4 0 obj"]:
        idx = search.find(marker)
        offsets.append(idx)

    xref = b"xref\n"
    xref += f"0 5\n".encode()
    xref += b"0000000000 65535 f \n"
    for off in offsets:
        xref += f"{off:010d} 00000 n \n".encode()

    trailer = (
        f"trailer\n<< /Size 5 /Root 1 0 R >>\n"
        f"startxref\n{xref_pos}\n"
        f"%%EOF\n"
    ).encode()

    return body + xref + trailer


badges = {
    "badge_synced.imageset/badge_synced.pdf": (0.20, 0.78, 0.35),   # green
    "badge_remote.imageset/badge_remote.pdf": (0.60, 0.60, 0.60),   # grey
}

script_dir = os.path.dirname(os.path.abspath(__file__))

for rel_path, color in badges.items():
    out_path = os.path.join(script_dir, rel_path)
    os.makedirs(os.path.dirname(out_path), exist_ok=True)
    pdf_bytes = make_badge_pdf(*color)
    with open(out_path, "wb") as f:
        f.write(pdf_bytes)
    print(f"Written {out_path}  ({len(pdf_bytes)} bytes)")

print("Done.")
