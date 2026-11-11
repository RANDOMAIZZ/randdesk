"""Собирает иконки RANDSEC из assets/icon.svg и assets/icon-small.svg.

    python assets/make_icons.py

Нужны Microsoft Edge (отрисовка SVG) и Pillow. Получается:
  assets/randsec.ico         — иконка (16…256 px; до 32 px — упрощённый рисунок)
  cmd/randsec/rsrc_windows_*.syso — та же иконка, вшивается в randsec.exe при сборке
  assets/icon-512.png        — для README/релизов
  web/static/icon-192.png    — для вкладки браузера и ярлыка на телефоне
"""
import io
import os
import struct
import subprocess
import tempfile

from PIL import Image

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
EDGE = r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"


def render(svg: str, size: int) -> Image.Image:
    """Отрисовывает SVG в квадрат size×size с прозрачным фоном."""
    with tempfile.TemporaryDirectory() as tmp:
        html = os.path.join(tmp, "i.html")
        url = "file:///" + os.path.join(ROOT, "assets", svg).replace("\\", "/")
        with open(html, "w", encoding="utf-8") as f:
            f.write(f'<html><body style="margin:0;background:transparent">'
                    f'<img src="{url}" style="width:{size}px;height:{size}px;display:block"></body></html>')
        png = os.path.join(tmp, "o.png")
        subprocess.run([EDGE, "--headless=new", f"--user-data-dir={os.path.join(tmp, 'p')}", "--disable-gpu",
                        "--hide-scrollbars", "--default-background-color=00000000",
                        f"--window-size={size},{size}", f"--screenshot={png}", "file:///" + html.replace("\\", "/")],
                       check=True, capture_output=True)
        return Image.open(png).convert("RGBA").copy()


def ico(images: list[Image.Image], path: str) -> None:
    """ICO с PNG внутри (Windows Vista+), по одному кадру на размер."""
    blobs = []
    for im in images:
        b = io.BytesIO()
        im.save(b, "PNG", optimize=True)
        blobs.append(b.getvalue())
    out = struct.pack("<HHH", 0, 1, len(images))
    off = 6 + 16 * len(images)
    for im, b in zip(images, blobs):
        w = im.width if im.width < 256 else 0
        out += struct.pack("<BBBBHHII", w, w, 0, 0, 1, 32, len(b), off)
        off += len(b)
    with open(path, "wb") as f:
        f.write(out + b"".join(blobs))


def syso(images: list[Image.Image], machine: int, reloc_type: int, path: str) -> None:
    """Объектный файл COFF с разделом .rsrc (RT_ICON + RT_GROUP_ICON): компоновщик Go сам
    вшивает .syso из папки пакета в exe, и Проводник показывает эту иконку."""
    pngs = []
    for im in images:
        b = io.BytesIO()
        im.save(b, "PNG", optimize=True)
        pngs.append(b.getvalue())
    n = len(images)
    group = struct.pack("<HHH", 0, 1, n) + b"".join(
        struct.pack("<BBBBHHIH", im.width % 256, im.height % 256, 0, 0, 1, 32, len(p), i + 1)
        for i, (im, p) in enumerate(zip(images, pngs)))

    # дерево ресурсов: тип → имя (ID) → язык → данные
    def dir_hdr(cnt): return struct.pack("<IIHHHH", 0, 0, 0, 0, 0, cnt)
    LANG = 0x0409
    leaves = [(3, i + 1, pngs[i]) for i in range(n)] + [(14, 1, group)]
    # раскладка: корень | каталоги типов | каталоги имён | записи данных | данные
    root_sz = 16 + 8 * 2
    type_dirs = {3: n, 14: 1}
    off = root_sz
    type_off = {}
    for t in (3, 14):
        type_off[t] = off
        off += 16 + 8 * type_dirs[t]
    name_off = {}
    for t, i, _ in leaves:
        name_off[(t, i)] = off
        off += 16 + 8
    data_entry_off = {}
    for t, i, _ in leaves:
        data_entry_off[(t, i)] = off
        off += 16
    data_off = {}
    for t, i, d in leaves:
        off = (off + 7) & ~7
        data_off[(t, i)] = off
        off += len(d)
    sec = bytearray(off)
    sec[0:16] = dir_hdr(2)
    for k, t in enumerate((3, 14)):
        struct.pack_into("<II", sec, 16 + 8 * k, t, 0x80000000 | type_off[t])
    for t in (3, 14):
        ids = [i for tt, i, _ in leaves if tt == t]
        o = type_off[t]
        sec[o:o + 16] = dir_hdr(len(ids))
        for k, i in enumerate(ids):
            struct.pack_into("<II", sec, o + 16 + 8 * k, i, 0x80000000 | name_off[(t, i)])
    relocs = []
    for t, i, d in leaves:
        o = name_off[(t, i)]
        sec[o:o + 16] = dir_hdr(1)
        struct.pack_into("<II", sec, o + 16, LANG, data_entry_off[(t, i)])
        e = data_entry_off[(t, i)]
        struct.pack_into("<IIII", sec, e, data_off[(t, i)], len(d), 0, 0)  # адрес исправит компоновщик
        relocs.append(e)
        sec[data_off[(t, i)]:data_off[(t, i)] + len(d)] = d
    sec = bytes(sec)
    raw_ptr = 20 + 40
    reloc_ptr = raw_ptr + len(sec)
    sym_ptr = reloc_ptr + 10 * len(relocs)
    hdr = struct.pack("<HHIIIHH", machine, 1, 0, sym_ptr, 1, 0, 0)
    shdr = struct.pack("<8sIIIIIIHHI", b".rsrc", 0, 0, len(sec), raw_ptr, reloc_ptr, 0, len(relocs), 0, 0x40000040)
    rel = b"".join(struct.pack("<IIH", r, 0, reloc_type) for r in relocs)
    sym = struct.pack("<8sIhHBB", b".rsrc", 0, 1, 0, 3, 0)  # символ раздела
    with open(path, "wb") as f:
        f.write(hdr + shdr + sec + rel + sym + struct.pack("<I", 4))


def main() -> None:
    big = render("icon.svg", 1024)
    small = render("icon-small.svg", 256)
    frames = []
    for s in (16, 20, 24, 32, 40, 48, 64, 128, 256):
        src = small if s <= 32 else big
        frames.append(src.resize((s, s), Image.LANCZOS))
    ico(frames, os.path.join(ROOT, "assets", "randsec.ico"))
    cmd = os.path.join(ROOT, "cmd", "randsec")
    syso(frames, 0x8664, 3, os.path.join(cmd, "rsrc_windows_amd64.syso"))  # IMAGE_REL_AMD64_ADDR32NB
    syso(frames, 0xAA64, 2, os.path.join(cmd, "rsrc_windows_arm64.syso"))  # IMAGE_REL_ARM64_ADDR32NB
    big.resize((512, 512), Image.LANCZOS).save(os.path.join(ROOT, "assets", "icon-512.png"), optimize=True)
    big.resize((192, 192), Image.LANCZOS).save(os.path.join(ROOT, "web", "static", "icon-192.png"), optimize=True)
    print("готово:", ", ".join(f"{f.width}" for f in frames))


if __name__ == "__main__":
    main()
