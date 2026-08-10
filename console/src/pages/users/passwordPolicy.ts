export const MAX_LOCAL_PASSWORD_BYTES = 72;

export function localPasswordByteLength(value: string): number {
  return new TextEncoder().encode(value).byteLength;
}

export function localPasswordCharacterLength(value: string): number {
  return Array.from(value).length;
}

export function localPasswordMeetsMinimum(value: string): boolean {
  return localPasswordCharacterLength(value) >= 8;
}

export function localPasswordFitsBcrypt(value: string): boolean {
  return localPasswordByteLength(value) <= MAX_LOCAL_PASSWORD_BYTES;
}
