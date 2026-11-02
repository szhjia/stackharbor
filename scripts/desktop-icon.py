"""Generate the original small stack/harbor icon without image dependencies."""
import pathlib
import struct
import subprocess
import sys
import zlib
root = pathlib.Path(sys.argv[1])
root.mkdir(parents=True, exist_ok=True)

def png(size):
    rows = bytearray()
    for y in range(size):
        rows.append(0)
        for x in range(size):
            u, v = x / size, y / size
            inside = .08 <= u <= .92 and .08 <= v <= .92
            color = (18, 35, 48, 255) if inside else (0, 0, 0, 0)
            if inside and ((.23 <= u <= .39 or .61 <= u <= .77) and .23 <= v <= .69
                           or .34 <= u <= .66 and .44 <= v <= .56
                           or .20 <= u <= .80 and .73 <= v <= .80):
                color = (95, 221, 192, 255)
            rows.extend(color)
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xffffffff)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(rows)) + chunk(b"IEND", b"")

(root / "icon.png").write_bytes(png(256))
if sys.platform == "darwin":
    iconset = root / "icon.iconset"
    iconset.mkdir(exist_ok=True)
    for size in (16, 32, 128, 256, 512):
        (iconset / f"icon_{size}x{size}.png").write_bytes(png(size))
        (iconset / f"icon_{size}x{size}@2x.png").write_bytes(png(size * 2))
    subprocess.run(["iconutil", "-c", "icns", str(iconset), "-o", str(root / "icon.icns")], check=True)
