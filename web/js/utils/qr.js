// Renders a standards-compliant QR code onto a canvas using the vendored
// qrcode-generator library (js/vendor/qrcode.js, loaded as a classic script).

export function renderQRCode(canvas, text) {
  const qrcode = window.qrcode;
  if (!qrcode) throw new Error('QR library failed to load');

  // Type 0 picks the smallest version that fits; level M survives smudged screens.
  const qr = qrcode(0, 'M');
  qr.addData(text, 'Byte');
  qr.make();

  const count = qr.getModuleCount();
  const quietZone = 4;
  const totalModules = count + quietZone * 2;

  // Draw on whole-pixel modules so phone cameras see crisp edges.
  const cssSize = canvas.clientWidth || canvas.width;
  const dpr = window.devicePixelRatio || 1;
  const cell = Math.max(2, Math.floor((cssSize * dpr) / totalModules));
  const size = cell * totalModules;
  canvas.width = size;
  canvas.height = size;

  const ctx = canvas.getContext('2d');
  ctx.fillStyle = '#ffffff';
  ctx.fillRect(0, 0, size, size);
  ctx.fillStyle = '#000000';
  for (let r = 0; r < count; r++) {
    for (let c = 0; c < count; c++) {
      if (qr.isDark(r, c)) {
        ctx.fillRect((c + quietZone) * cell, (r + quietZone) * cell, cell, cell);
      }
    }
  }
}
