"""Сборка значков MS7VPN.

Pillow при сохранении .ico со списком sizes просто ужимает одно исходное
изображение под каждый размер, из-за чего знак на 16 пикселях превращался
в пятно. Здесь каждый размер отрисовывается отдельно, а формат .ico
пишется вручную.
"""
import struct
from io import BytesIO
from PIL import Image

mark = Image.open('/home/claude/mark.png').convert('RGBA')


def frame(size, tint=None):
    """Знак на прозрачном фоне, вписанный в квадрат значка."""
    scale = min(size / mark.width, size / mark.height) * 0.96
    art = mark.resize((max(1, round(mark.width * scale)),
                       max(1, round(mark.height * scale))), Image.LANCZOS)
    canvas = Image.new('RGBA', (size, size), (0, 0, 0, 0))
    canvas.paste(art, ((size - art.width) // 2, (size - art.height) // 2), art)
    if tint is not None:
        pixels = canvas.load()
        for y in range(size):
            for x in range(size):
                r, g, b, a = pixels[x, y]
                if a:
                    pixels[x, y] = (*tint, a)
    return canvas


def dib_bytes(image):
    """Классическая запись значка: BITMAPINFOHEADER + BGRA + пустая маска."""
    w, h = image.size
    header = struct.pack('<IiiHHIIiiII', 40, w, h * 2, 1, 32, 0, 0, 0, 0, 0, 0)
    pixels = image.load()
    xor = bytearray()
    for y in range(h - 1, -1, -1):          # снизу вверх, как требует формат
        for x in range(w):
            r, g, b, a = pixels[x, y]
            xor += bytes((b, g, r, a))
    row = ((w + 31) // 32) * 4              # маска: 1 бит на пиксель, ряд кратен 4 байтам
    return header + bytes(xor) + bytes(row * h)


def png_bytes(image):
    buffer = BytesIO()
    image.save(buffer, format='PNG')
    return buffer.getvalue()


def write_ico(path, frames):
    entries, blobs = [], []
    offset = 6 + 16 * len(frames)
    for size, image in frames:
        data = png_bytes(image) if size >= 64 else dib_bytes(image)
        entries.append((size, len(data), offset))
        blobs.append(data)
        offset += len(data)

    with open(path, 'wb') as out:
        out.write(struct.pack('<HHH', 0, 1, len(frames)))
        for size, length, position in entries:
            byte = 0 if size >= 256 else size
            out.write(struct.pack('<BBBBHHII', byte, byte, 0, 0, 1, 32, length, position))
        for blob in blobs:
            out.write(blob)


frames = [(size, frame(size)) for size in (16, 20, 24, 32, 40, 48, 64, 128, 256)]

for path in ('/home/claude/ms7app/assets/MS7VPN.ico',
             '/home/claude/ms7app/cmd/installer/MS7VPN.ico',
             '/home/claude/ms7app/internal/winui/MS7VPN.ico',
             '/home/claude/ms7app/internal/webui/icon/MS7VPN.ico'):
    write_ico(path, frames)
    print('значок записан:', path)

frames[-1][1].save('/home/claude/ms7app/assets/icon.png')

# Значки состояния для области уведомлений — тот же знак, разный цвет.
for name, tint in (('on', None), ('connecting', (255, 176, 46)), ('off', (150, 150, 165))):
    frame(64, tint).save(f'/home/claude/ms7app/internal/tray/icons/{name}.png')
    print('значок трея:', name)
