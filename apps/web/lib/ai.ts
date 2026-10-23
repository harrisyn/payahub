// Plain names for the tasks a model can serve, shared by the server page and the form.
export const taskName: Record<string, [string, string]> = {
  document_intake: [
    "Reading claim documents",
    "Turns a scanned form or invoice into claim fields, each with its evidence.",
  ],
  code_matching: [
    "Matching item codes",
    "Suggests the standard item for a clinic's own service or drug name.",
  ],
};
