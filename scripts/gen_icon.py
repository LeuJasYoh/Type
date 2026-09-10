# gen_icon.py — 图标资产生成管线
# 用法: uv run scripts/gen_icon.py
#
# assets/icon.jpg (322×322 源图) → 圆角正方形渲染:
#   assets/winres/{16..256}x{same}.png  七档尺寸 PNG
#   assets/icon.ico                     多尺寸 ICO (目录降序, 256 在前)
#
# 渲染参数经对 v1.3.4 既有产物的逆向标定, 生成结果与其字节级一致:
#   - 圆角半径 57 (322 源图坐标系), rounded_rectangle 硬边掩膜后整体缩小
#   - LANCZOS 从源图单次缩放到目标尺寸 (不做 322→256→N 级联)
#   - 不携带源图 ICC 配置 (剥除 iCCP 块)
#
# ICO 容器手工组装: Pillow 各版本对目录项排序/帧编码策略不一
# (12.x 强制升序, 生成本产物集的旧版为基图优先), 自行写入使产物
# 不随 Pillow 升级漂移。帧体为 PNG, 与 winres 各档完全同字节。
from __future__ import annotations

import io
import struct
from pathlib import Path

from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parents[1]
SRC = ROOT / "assets" / "icon.jpg"
SIZES = [16, 24, 32, 48, 64, 128, 256]
RADIUS = 57  # 322 源图坐标系下的圆角半径


def render(size: int) -> bytes:
    """渲染单档尺寸并编码为 PNG 字节"""
    src = Image.open(SRC).convert("RGBA")
    mask = Image.new("L", src.size, 0)
    ImageDraw.Draw(mask).rounded_rectangle(
        [0, 0, src.width - 1, src.height - 1], radius=RADIUS, fill=255
    )
    img = src.copy()
    img.putalpha(mask)
    img.info.pop("icc_profile", None)  # 不携带源图色彩配置
    img = img.resize((size, size), Image.LANCZOS)
    buf = io.BytesIO()
    img.save(buf, format="PNG")
    return buf.getvalue()


def build_ico(frames: dict[int, bytes]) -> bytes:
    """组装 ICO: 6 字节头 + 16 字节目录项×N (降序) + PNG 帧体拼接"""
    order = sorted(frames, reverse=True)
    out = struct.pack("<HHH", 0, 1, len(order))  # reserved / type=ICO / count
    offset = 6 + 16 * len(order)
    body = b""
    for s in order:
        data = frames[s]
        dim = 0 if s >= 256 else s  # 目录项中 0 表示 256
        out += struct.pack("<BBBBHHII", dim, dim, 0, 0, 1, 32, len(data), offset)
        body += data
        offset += len(data)
    return out + body


def main() -> None:
    frames: dict[int, bytes] = {}
    for s in SIZES:
        data = render(s)
        frames[s] = data
        path = ROOT / "assets" / "winres" / f"{s}x{s}.png"
        path.write_bytes(data)
        print(f"{path.relative_to(ROOT)}  {len(data)} B")
    ico = build_ico(frames)
    (ROOT / "assets" / "icon.ico").write_bytes(ico)
    print(f"assets/icon.ico  {len(ico)} B")


if __name__ == "__main__":
    main()
