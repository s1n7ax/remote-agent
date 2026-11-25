// Ticket images: the client-side checks and conversions done before an
// upload. The server enforces the same limits and sniffs the type itself.

import type { AgentKind, AppState } from './types';

export const MAX_IMAGES = 10;
export const MAX_IMAGE_MB = 20;
const MAX_IMAGE_SIZE = MAX_IMAGE_MB << 20;

const ACCEPTED = new Set(['image/jpeg', 'image/png', 'image/webp', 'image/gif']);

export const NOT_IMAGE = 'Only images (JPEG, PNG, WebP, GIF) can be attached.';
export const TOO_MANY = `A ticket can have at most ${MAX_IMAGES} images.`;
export const TOO_BIG = `Each image can be at most ${MAX_IMAGE_MB} MB.`;
export const NO_HEIC = "This browser can't read HEIC photos; export it as JPEG first.";

/** An iPhone photo the browser handed over unconverted. */
export function isHeic(file: File): boolean {
  return /^image\/hei[cf]/.test(file.type) || /\.hei[cf]$/i.test(file.name);
}

/**
 * Says why `file` cannot be added to `count` images already there, or null
 * when it can. Files with no type are left to the server to sniff.
 */
export function refusal(file: File, count: number): string | null {
  if (count >= MAX_IMAGES) return TOO_MANY;
  if (file.type && !ACCEPTED.has(file.type)) return NOT_IMAGE;
  if (file.size > MAX_IMAGE_SIZE) return TOO_BIG;
  return null;
}

/** Re-encodes a HEIC photo as JPEG where the browser can decode it (Safari); other files pass through. */
export async function prepareImage(file: File): Promise<File> {
  if (!isHeic(file)) return file;
  const url = URL.createObjectURL(file);
  try {
    const img = new Image();
    img.src = url;
    await img.decode();
    const canvas = document.createElement('canvas');
    canvas.width = img.naturalWidth;
    canvas.height = img.naturalHeight;
    canvas.getContext('2d')?.drawImage(img, 0, 0);
    const blob = await new Promise<Blob | null>((ok) => canvas.toBlob(ok, 'image/jpeg', 0.92));
    if (!blob) throw new Error(NO_HEIC);
    return new File([blob], file.name.replace(/\.hei[cf]$/i, '') + '.jpg', { type: 'image/jpeg' });
  } catch {
    throw new Error(NO_HEIC);
  } finally {
    URL.revokeObjectURL(url);
  }
}

/** The files in a drop. */
export function filesOf(data: DataTransfer | null): File[] {
  return data ? Array.from(data.files) : [];
}

/**
 * The images to attach from a paste: its files, unless it also carries text
 * (Office apps put a picture of copied cells next to the text), in which
 * case the paste is left to insert the text.
 */
export function pastedFiles(data: DataTransfer | null): File[] {
  if (!data || data.types.includes('text/plain')) return [];
  return filesOf(data);
}

/** Whether an agent running `model` can look at images (see AppState.imageModels). */
export function seesImages(state: AppState | null, agent: AgentKind, model: string): boolean {
  const list = state?.imageModels?.[agent];
  if (list === undefined) return true;
  return (list ?? []).includes(model.replace(/^~/, ''));
}

/** The warning shown when the chosen model cannot look at the ticket's images. */
export function blindWarning(count: number): string {
  return `This model can't look at images. The agent will be told about your ${count} image${count === 1 ? '' : 's'} but won't see ${count === 1 ? 'it' : 'them'}.`;
}
