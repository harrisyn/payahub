"use client";

import {
  Loader,
  MultiSelect as MMultiSelect,
  Select as MSelect,
  TagsInput as MTagsInput,
  type ComboboxItem,
  type MultiSelectProps as MMultiSelectProps,
  type SelectProps as MSelectProps,
  type TagsInputProps as MTagsInputProps,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useEffect, useMemo, useRef, useState } from "react";

import type { FrameProps } from "./inputs";

export type Option = {
  value: string;
  label: string;
  description?: string;
  disabled?: boolean;
};

const NOTHING = "Nothing matches";

// Lists open below the field, scroll after ~8 rows and show the hint text in
// a second muted line when an option carries one.
const shared = {
  nothingFoundMessage: NOTHING,
  maxDropdownHeight: 296,
  comboboxProps: { withinPortal: true, offset: 4 },
  renderOption: ({
    option,
  }: {
    option: ComboboxItem & { description?: string };
  }) => (
    <span className="ph-option">
      <span>{option.label}</span>
      {option.description && (
        <span className="ph-option__hint">{option.description}</span>
      )}
    </span>
  ),
};

// Values go to the form as one hidden input each, so a server action reads
// them with formData.getAll(name) and no value ever needs splitting.
function Hidden({ name, values }: { name?: string; values: string[] }) {
  if (!name) return null;
  return (
    <>
      {values.map((v) => (
        <input key={v} type="hidden" name={name} value={v} />
      ))}
    </>
  );
}

export type SelectProps = Omit<MSelectProps, "data" | "error" | "searchable"> &
  FrameProps & { data: Option[] | string[] };

/** One choice from a list held in the page. Always searchable. */
export function Select({ error, data, ...rest }: SelectProps) {
  // A choice stays chosen: clicking it again does not empty the field.
  return (
    <MSelect
      searchable
      allowDeselect={false}
      error={error}
      data={data}
      {...shared}
      {...rest}
    />
  );
}

export type MultiSelectProps = Omit<
  MMultiSelectProps,
  "data" | "error" | "searchable" | "name"
> &
  FrameProps & { data: Option[] | string[]; name?: string };

/** Several choices from a list held in the page. Always searchable. */
export function MultiSelect({
  error,
  data,
  name,
  value,
  defaultValue,
  onChange,
  ...rest
}: MultiSelectProps) {
  const [picked, setPicked] = useState<string[]>(value ?? defaultValue ?? []);
  const current = value ?? picked;
  return (
    <>
      <MMultiSelect
        searchable
        hidePickedOptions
        error={error}
        data={data}
        value={current}
        onChange={(v) => {
          setPicked(v);
          onChange?.(v);
        }}
        {...shared}
        {...rest}
      />
      <Hidden name={name} values={current} />
    </>
  );
}

// ---------- remote type-ahead ----------

/** Lists the type-ahead can search; each is served by app/lookup/[kind]. */
export type LookupSource =
  | "items"
  | "medicines"
  | "services"
  | "diagnoses"
  | "payers";

// useLookup asks the web server for options matching what was typed. Typing is
// debounced, a newer query cancels the one in flight, and answers are cached
// for the life of the field so backspacing does not refetch.
function useLookup(source: LookupSource, search: string, minChars: number) {
  const [query] = useDebouncedValue(search.trim(), 220);
  const cache = useRef(new Map<string, Option[]>());
  const [options, setOptions] = useState<Option[]>([]);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    if (query.length < minChars) {
      setOptions([]);
      return;
    }
    const hit = cache.current.get(query);
    if (hit) {
      setOptions(hit);
      return;
    }
    const ctl = new AbortController();
    setLoading(true);
    setFailed(false);
    fetch(`/lookup/${source}?q=${encodeURIComponent(query)}`, {
      signal: ctl.signal,
      headers: { Accept: "application/json" },
    })
      .then((r) =>
        r.ok ? r.json() : Promise.reject(new Error(String(r.status))),
      )
      .then((body: { data: Option[] }) => {
        cache.current.set(query, body.data);
        setOptions(body.data);
      })
      .catch((e: unknown) => {
        if ((e as Error).name !== "AbortError") setFailed(true);
      })
      .finally(() => {
        if (!ctl.signal.aborted) setLoading(false);
      });
    return () => ctl.abort();
  }, [source, query, minChars]);

  return { options, loading, failed, waiting: search.trim().length < minChars };
}

function message(
  loading: boolean,
  failed: boolean,
  waiting: boolean,
  minChars: number,
) {
  if (loading) return "Searching…";
  if (failed) return "Search is unavailable. Try again.";
  if (waiting)
    return `Type ${minChars === 1 ? "a letter" : `${minChars} letters`} to search`;
  return NOTHING;
}

// Server-side results are already ranked, so the client keeps them as given.
const keepAll = ({ options }: { options: unknown[] }) => options as never;

export type RemoteSelectProps = Omit<
  SelectProps,
  "data" | "onChange" | "value" | "defaultValue"
> & {
  source: LookupSource;
  /** The current choice with its label, so the field can show it before any search. */
  defaultOption?: Option | null;
  onChange?: (option: Option | null) => void;
  minChars?: number;
};

/** One choice, searched on the server as the person types (type-ahead). */
export function RemoteSelect({
  source,
  defaultOption = null,
  onChange,
  minChars = 1,
  error,
  ...rest
}: RemoteSelectProps) {
  const [chosen, setChosen] = useState<Option | null>(defaultOption);
  const [search, setSearch] = useState("");
  const { options, loading, failed, waiting } = useLookup(
    source,
    search,
    minChars,
  );
  // The chosen option stays in the list so the field can render its label.
  const data = useMemo(() => {
    if (!chosen || options.some((o) => o.value === chosen.value))
      return options;
    return [chosen, ...options];
  }, [options, chosen]);

  return (
    <MSelect
      searchable
      clearable
      error={error}
      data={data}
      value={chosen?.value ?? null}
      onChange={(v) => {
        const next =
          v == null ? null : (data.find((o) => o.value === v) ?? null);
        setChosen(next);
        onChange?.(next);
      }}
      searchValue={search}
      onSearchChange={setSearch}
      filter={keepAll}
      rightSection={loading ? <Loader size={14} /> : undefined}
      {...shared}
      nothingFoundMessage={message(loading, failed, waiting, minChars)}
      {...rest}
    />
  );
}

export type RemoteMultiSelectProps = Omit<
  MultiSelectProps,
  "data" | "onChange" | "value" | "defaultValue"
> & {
  source: LookupSource;
  defaultOptions?: Option[];
  onChange?: (options: Option[]) => void;
  minChars?: number;
};

/** Several choices, searched on the server as the person types. */
export function RemoteMultiSelect({
  source,
  name,
  defaultOptions = [],
  onChange,
  minChars = 1,
  error,
  ...rest
}: RemoteMultiSelectProps) {
  const [chosen, setChosen] = useState<Option[]>(defaultOptions);
  const [search, setSearch] = useState("");
  const { options, loading, failed, waiting } = useLookup(
    source,
    search,
    minChars,
  );
  const data = useMemo(() => {
    const seen = new Set(options.map((o) => o.value));
    return [...options, ...chosen.filter((o) => !seen.has(o.value))];
  }, [options, chosen]);

  return (
    <>
      <MMultiSelect
        searchable
        clearable
        hidePickedOptions
        error={error}
        data={data}
        value={chosen.map((o) => o.value)}
        onChange={(values) => {
          const next = values
            .map((v) => data.find((o) => o.value === v))
            .filter((o): o is Option => !!o);
          setChosen(next);
          onChange?.(next);
        }}
        searchValue={search}
        onSearchChange={setSearch}
        filter={keepAll}
        rightSection={loading ? <Loader size={14} /> : undefined}
        {...shared}
        nothingFoundMessage={message(loading, failed, waiting, minChars)}
        {...rest}
      />
      <Hidden name={name} values={chosen.map((o) => o.value)} />
    </>
  );
}

// ---------- tags ----------

export type TagsInputProps = Omit<MTagsInputProps, "error" | "name"> &
  FrameProps & { name?: string };

/** Free-form tags: Enter or a comma adds one; suggestions are optional. */
export function TagsInput({
  error,
  name,
  value,
  defaultValue,
  onChange,
  splitChars = [",", ";"],
  ...rest
}: TagsInputProps) {
  const [tags, setTags] = useState<string[]>(value ?? defaultValue ?? []);
  const current = value ?? tags;
  return (
    <>
      <MTagsInput
        error={error}
        clearable
        splitChars={splitChars}
        value={current}
        onChange={(v) => {
          setTags(v);
          onChange?.(v);
        }}
        comboboxProps={shared.comboboxProps}
        maxDropdownHeight={shared.maxDropdownHeight}
        {...rest}
      />
      <Hidden name={name} values={current} />
    </>
  );
}
