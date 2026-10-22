"use client";

import {
  DateInput as MDateInput,
  DatePickerInput,
  DateTimePicker as MDateTimePicker,
  type DateInputProps as MDateInputProps,
  type DatePickerInputProps,
  type DateTimePickerProps as MDateTimePickerProps,
} from "@mantine/dates";
import dayjs from "dayjs";
import customParseFormat from "dayjs/plugin/customParseFormat";
import { useState } from "react";

import type { FrameProps } from "./inputs";

// Dates travel as ISO strings: "2026-10-20" for a day, "2026-10-20 14:30:00"
// for a moment. Display follows Ghana's day-first order.
const DAY = "D MMM YYYY";

dayjs.extend(customParseFormat);
const TYPED = [DAY, "D MMMM YYYY", "D/M/YYYY", "D-M-YYYY", "D.M.YYYY", "YYYY-MM-DD"];

// parseDay reads what people type; anything unreadable leaves the field empty
// rather than guessing month-first.
function parseDay(input: string) {
  const d = dayjs(input.trim(), TYPED, true);
  return d.isValid() ? d.format("YYYY-MM-DD") : null;
}

export type DateInputProps = Omit<MDateInputProps, "error"> & FrameProps;

/** A day, typed ("20 Oct 2026", "20/10/2026") or picked from the calendar. */
export function DateInput({ error, valueFormat = DAY, ...rest }: DateInputProps) {
  return <MDateInput error={error} valueFormat={valueFormat} dateParser={parseDay} clearable popoverProps={{ withinPortal: true }} {...rest} />;
}

export type DateRangeInputProps = Omit<DatePickerInputProps<"range">, "type" | "error" | "name" | "value" | "defaultValue"> &
  FrameProps & {
    /** Sends `${name}_from` and `${name}_to` as YYYY-MM-DD. */
    name?: string;
    defaultValue?: [string | null, string | null];
  };

/** A from–to range in one field: pick the first day, then the last. */
export function DateRangeInput({ error, name, defaultValue = [null, null], onChange, valueFormat = DAY, ...rest }: DateRangeInputProps) {
  const [range, setRange] = useState<[string | null, string | null]>(defaultValue);
  return (
    <>
      <DatePickerInput
        type="range"
        error={error}
        valueFormat={valueFormat}
        clearable
        allowSingleDateInRange
        value={range}
        onChange={(v) => {
          setRange(v as [string | null, string | null]);
          onChange?.(v);
        }}
        popoverProps={{ withinPortal: true }}
        {...rest}
      />
      {name && (
        <>
          <input type="hidden" name={`${name}_from`} value={range[0] ?? ""} />
          <input type="hidden" name={`${name}_to`} value={range[1] ?? ""} />
        </>
      )}
    </>
  );
}

export type DateTimeInputProps = Omit<MDateTimePickerProps, "error"> & FrameProps;

/** A day and a time, such as when a patient was admitted. */
export function DateTimeInput({ error, valueFormat = `${DAY}, HH:mm`, ...rest }: DateTimeInputProps) {
  return <MDateTimePicker error={error} valueFormat={valueFormat} clearable popoverProps={{ withinPortal: true }} {...rest} />;
}
