/** The artifact id a proof report image points at, or null when the image is unresolved. */
export function proofArtifactId(src: string | null | undefined): string | null {
  const m = /^\/api\/artifacts\/([A-Za-z0-9_-]+)$/.exec(src ?? '');
  return m ? m[1] : null;
}
