// SMS segment counting: GSM-7 (160 / 153 per part) vs UCS-2 Unicode (70 / 67 per part).
const GSM_7BIT = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞ\x1bÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ`abcdefghijklmnopqrstuvwxyzäöñüà";
const GSM_7BIT_EXT = '|^€{}[]~\\';

export interface SmsStats {
  length: number;
  encoding: 'GSM-7' | 'Unicode';
  parts: number;
  perPart: number;
}

export function analyzeSms(text: string): SmsStats {
  let weight = 0;
  for (const ch of text) {
    if (GSM_7BIT.includes(ch)) weight += 1;
    else if (GSM_7BIT_EXT.includes(ch)) weight += 2;
    else {
      const len = Array.from(text).length;
      const multi = len > 70;
      return { length: len, encoding: 'Unicode', parts: multi ? Math.ceil(len / 67) : 1, perPart: multi ? 67 : 70 };
    }
  }
  const multi = weight > 160;
  return { length: weight, encoding: 'GSM-7', parts: multi ? Math.ceil(weight / 153) : 1, perPart: multi ? 153 : 160 };
}
