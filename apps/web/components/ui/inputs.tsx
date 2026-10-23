"use client";

import {
  Checkbox as MCheckbox,
  Input,
  NumberInput as MNumberInput,
  PasswordInput as MPasswordInput,
  SegmentedControl as MSegmentedControl,
  Switch as MSwitch,
  Textarea as MTextarea,
  TextInput as MTextInput,
  type NumberInputProps as MNumberInputProps,
  type PasswordInputProps as MPasswordInputProps,
  type TextareaProps as MTextareaProps,
  type TextInputProps as MTextInputProps,
} from "@mantine/core";
import { useId } from "@mantine/hooks";
import { useState, type ReactNode } from "react";

// Every kit field takes the same frame: label, description, error, required.
// Errors are strings so a server action's field errors drop straight in.
export type FrameProps = {
  label?: ReactNode;
  description?: ReactNode;
  error?: string;
  required?: boolean;
};

type Addons = {
  /** Attached box before the field, such as "GHS" or "https://". */
  prepend?: ReactNode;
  /** Attached box after the field, such as ".payahub.com" or "per claim". */
  append?: ReactNode;
};

// Grouped renders the attached boxes around a borderless inner field and
// keeps one label and one error for the whole group.
function Grouped({
  id,
  label,
  description,
  error,
  required,
  prepend,
  append,
  children,
}: FrameProps & Addons & { id: string; children: ReactNode }) {
  return (
    <Input.Wrapper id={id} label={label} description={description} error={error} required={required}>
      <div className="ph-group" data-error={error ? "" : undefined}>
        {prepend != null && <span className="ph-addon">{prepend}</span>}
        {children}
        {append != null && <span className="ph-addon">{append}</span>}
      </div>
    </Input.Wrapper>
  );
}

export type TextInputProps = Omit<MTextInputProps, "error"> & FrameProps & Addons;

/** A one-line text field, with optional attached prepend/append boxes and in-field icons (leftSection/rightSection). */
export function TextInput({ prepend, append, label, description, error, required, id, ...rest }: TextInputProps) {
  const uid = useId(id);
  if (prepend == null && append == null) {
    return <MTextInput id={uid} label={label} description={description} error={error} required={required} {...rest} />;
  }
  return (
    <Grouped id={uid} {...{ label, description, error, required, prepend, append }}>
      <MTextInput id={uid} required={required} error={!!error} className="ph-group__field" {...rest} />
    </Grouped>
  );
}

export type NumberInputProps = Omit<MNumberInputProps, "error"> & FrameProps & Addons;

export function NumberInput({ prepend, append, label, description, error, required, id, ...rest }: NumberInputProps) {
  const uid = useId(id);
  if (prepend == null && append == null) {
    return <MNumberInput id={uid} label={label} description={description} error={error} required={required} {...rest} />;
  }
  return (
    <Grouped id={uid} {...{ label, description, error, required, prepend, append }}>
      <MNumberInput id={uid} required={required} error={!!error} className="ph-group__field" {...rest} />
    </Grouped>
  );
}

export type MoneyInputProps = Omit<NumberInputProps, "name" | "value" | "defaultValue" | "onChange"> & {
  name: string;
  /** Amount in minor units (pesewas), as the API stores money. */
  defaultMinor?: number | null;
  currency?: string;
  onChangeMinor?: (minor: number | null) => void;
};

/**
 * A money field: people type cedis, the form sends whole minor units under
 * `name`, so a server action never parses "1,250.50".
 */
export function MoneyInput({ name, defaultMinor, currency = "GHS", onChangeMinor, ...rest }: MoneyInputProps) {
  const [major, setMajor] = useState<number | string>(defaultMinor == null ? "" : defaultMinor / 100);
  const minor = typeof major === "number" ? Math.round(major * 100) : null;
  return (
    <>
      <NumberInput
        prepend={currency}
        decimalScale={2}
        fixedDecimalScale
        thousandSeparator=","
        min={0}
        hideControls
        inputMode="decimal"
        value={major}
        onChange={(v) => {
          setMajor(v);
          onChangeMinor?.(typeof v === "number" ? Math.round(v * 100) : null);
        }}
        {...rest}
      />
      <input type="hidden" name={name} value={minor ?? ""} />
    </>
  );
}

export type PasswordInputProps = Omit<MPasswordInputProps, "error"> & FrameProps;

export function PasswordInput({ error, ...rest }: PasswordInputProps) {
  return <MPasswordInput error={error} {...rest} />;
}

export type TextareaProps = Omit<MTextareaProps, "error"> & FrameProps;

/** Plain multi-line text that grows with its content. Use RichTextArea for formatted text. */
export function Textarea({ error, minRows = 3, maxRows = 12, ...rest }: TextareaProps) {
  return <MTextarea error={error} autosize minRows={minRows} maxRows={maxRows} {...rest} />;
}

export { MCheckbox as Checkbox, MSwitch as Switch, MSegmentedControl as SegmentedControl };
