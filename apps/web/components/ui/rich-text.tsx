"use client";

import { Input } from "@mantine/core";
import { useId } from "@mantine/hooks";
import { Link, RichTextEditor } from "@mantine/tiptap";
import Placeholder from "@tiptap/extension-placeholder";
import { useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { useState } from "react";

import type { FrameProps } from "./inputs";

export type RichTextAreaProps = FrameProps & {
  /** The form field that carries the HTML. */
  name?: string;
  defaultValue?: string;
  placeholder?: string;
  onChange?: (html: string) => void;
  /** "basic" for notes; "full" adds headings, quotes and code. */
  toolbar?: "basic" | "full";
  minHeight?: number;
};

/**
 * Formatted text (WYSIWYG) that submits HTML under `name`. The editor only
 * produces a small set of tags, but whatever stores or shows this HTML must
 * still sanitise it on the server: the browser is not a trust boundary.
 */
export function RichTextArea({
  name,
  label,
  description,
  error,
  required,
  defaultValue = "",
  placeholder,
  onChange,
  toolbar = "basic",
  minHeight = 140,
}: RichTextAreaProps) {
  const id = useId();
  const [html, setHtml] = useState(defaultValue);
  const editor = useEditor({
    // Render on the client only, so server and client markup always agree.
    immediatelyRender: false,
    extensions: [
      StarterKit.configure({ link: false, heading: { levels: [2, 3] } }),
      Link.configure({ openOnClick: false, autolink: true, defaultProtocol: "https" }),
      Placeholder.configure({ placeholder }),
    ],
    content: defaultValue,
    editorProps: {
      attributes: {
        id,
        role: "textbox",
        "aria-multiline": "true",
        ...(label ? { "aria-labelledby": `${id}-label` } : {}),
        ...(error ? { "aria-invalid": "true" } : {}),
      },
    },
    onUpdate: ({ editor }) => {
      const next = editor.isEmpty ? "" : editor.getHTML();
      setHtml(next);
      onChange?.(next);
    },
  });

  return (
    <Input.Wrapper
      label={label}
      labelProps={{ id: `${id}-label`, htmlFor: undefined, onClick: () => editor?.commands.focus() }}
      description={description}
      error={error}
      required={required}
    >
      <RichTextEditor editor={editor} className="ph-rich" data-error={error ? "" : undefined} style={{ ["--ph-rich-min" as string]: `${minHeight}px` }}>
        <RichTextEditor.Toolbar>
          {toolbar === "full" && (
            <RichTextEditor.ControlsGroup>
              <RichTextEditor.H2 />
              <RichTextEditor.H3 />
            </RichTextEditor.ControlsGroup>
          )}
          <RichTextEditor.ControlsGroup>
            <RichTextEditor.Bold />
            <RichTextEditor.Italic />
            <RichTextEditor.Underline />
            <RichTextEditor.Strikethrough />
          </RichTextEditor.ControlsGroup>
          <RichTextEditor.ControlsGroup>
            <RichTextEditor.BulletList />
            <RichTextEditor.OrderedList />
            {toolbar === "full" && <RichTextEditor.Blockquote />}
            {toolbar === "full" && <RichTextEditor.Code />}
          </RichTextEditor.ControlsGroup>
          <RichTextEditor.ControlsGroup>
            <RichTextEditor.Link />
            <RichTextEditor.Unlink />
          </RichTextEditor.ControlsGroup>
          <RichTextEditor.ControlsGroup>
            <RichTextEditor.Undo />
            <RichTextEditor.Redo />
          </RichTextEditor.ControlsGroup>
        </RichTextEditor.Toolbar>
        <RichTextEditor.Content />
      </RichTextEditor>
      {name && <input type="hidden" name={name} value={html} />}
    </Input.Wrapper>
  );
}
