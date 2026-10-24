// Standard GSM 7-bit default alphabet lookup
const GSM_7BIT_CHARS = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞ\x1bÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ`abcdefghijklmnopqrstuvwxyzäöñüà";
const GSM_7BIT_EXT = "|^€{}[]~\\";

export function analyzeSmsText(text) {
  if (!text) {
    return {
      length: 0,
      encoding: 'GSM-7',
      parts: 1,
      maxPerPart: 160,
      remainingInPart: 160,
      totalChars: 0
    };
  }

  let isUnicode = false;
  let gsmWeight = 0;

  for (let i = 0; i < text.length; i++) {
    const char = text[i];
    if (GSM_7BIT_CHARS.indexOf(char) !== -1) {
      gsmWeight += 1;
    } else if (GSM_7BIT_EXT.indexOf(char) !== -1) {
      gsmWeight += 2; // Extended escape chars count as 2
    } else {
      isUnicode = true;
      break;
    }
  }

  if (isUnicode) {
    const len = Array.from(text).length;
    let parts = 1;
    let maxPerPart = 70;
    if (len > 70) {
      maxPerPart = 67;
      parts = Math.ceil(len / 67);
    }
    const remainingInPart = (parts * maxPerPart) - len;
    return {
      length: len,
      encoding: 'UCS-2 (Unicode)',
      parts: parts,
      maxPerPart: maxPerPart,
      remainingInPart: Math.max(0, remainingInPart),
      totalChars: len
    };
  } else {
    let parts = 1;
    let maxPerPart = 160;
    if (gsmWeight > 160) {
      maxPerPart = 153;
      parts = Math.ceil(gsmWeight / 153);
    }
    const remainingInPart = (parts * maxPerPart) - gsmWeight;
    return {
      length: gsmWeight,
      encoding: 'GSM-7',
      parts: parts,
      maxPerPart: maxPerPart,
      remainingInPart: Math.max(0, remainingInPart),
      totalChars: text.length
    };
  }
}

