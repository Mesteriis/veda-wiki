/** The text of a named form field; absent fields and file inputs read as "". */
export function field(data: FormData, name: string): string {
  const value = data.get(name);
  return typeof value === "string" ? value : "";
}

/** The texts of every field with this name, such as checked checkboxes. */
export function fields(data: FormData, name: string): string[] {
  return data.getAll(name).filter((value): value is string => typeof value === "string");
}
