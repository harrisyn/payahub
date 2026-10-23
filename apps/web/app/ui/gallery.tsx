"use client";

import { useState, type FormEvent, type ReactNode } from "react";

import {
  Checkbox,
  DateInput,
  DateRangeInput,
  DateTimeInput,
  MoneyInput,
  MultiSelect,
  NumberInput,
  PasswordInput,
  RemoteMultiSelect,
  RemoteSelect,
  RichTextArea,
  SegmentedControl,
  Select,
  Switch,
  TagsInput,
  Textarea,
  TextInput,
  UIProvider,
} from "@/components/ui";

const insurers = [
  { value: "sandbox", label: "Sandbox insurer", description: "Test claims, no money moves" },
  { value: "xceli-a", label: "Insurer A", description: "Through XCeli" },
  { value: "medserve-b", label: "Insurer B", description: "Through Medserve" },
  { value: "smart-c", label: "Insurer C", description: "Through SmartClaims" },
  { value: "rx-d", label: "Insurer D", description: "Through RX Insurance" },
  { value: "claimiq-e", label: "Insurer E", description: "Through ClaimIQ" },
];

function Section({ title, note, children }: { title: string; note: string; children: ReactNode }) {
  return (
    <section className="ph-gallery__card">
      <div className="ph-gallery__card-head">
        <h2>{title}</h2>
        <p>{note}</p>
      </div>
      <div className="ph-gallery__grid">{children}</div>
    </section>
  );
}

export function Gallery() {
  const [sent, setSent] = useState<[string, string][] | null>(null);

  function preview(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const entries = [...new FormData(e.currentTarget).entries()].map(([k, v]) => [k, String(v)] as [string, string]);
    setSent(entries.filter(([, v]) => v !== ""));
  }

  return (
    <UIProvider>
      <form className="ph-gallery__form" onSubmit={preview}>
        <Section title="Text inputs" note="Prepend and append boxes, in-field icons, money in minor units.">
          <TextInput name="clinic" label="Clinic name" placeholder="Ridge Clinic" required />
          <TextInput name="site" label="Clinic website" prepend="https://" append=".com.gh" placeholder="ridgeclinic" />
          <MoneyInput name="price_minor" label="Unit price" description="Sent as pesewas" defaultMinor={15000} />
          <NumberInput name="days" label="Admission length" append="days" min={0} defaultValue={3} />
          <TextInput
            name="member"
            label="Member number"
            placeholder="EQ25834301945-0"
            leftSection={<span aria-hidden="true">#</span>}
            error="This member number is not on the insurer's list"
          />
          <PasswordInput name="secret" label="Insurer password" placeholder="Sealed when saved" />
        </Section>

        <Section title="Selects" note="All searchable. Remote ones ask the server as you type.">
          <Select name="payer" label="Insurer" placeholder="Pick one" data={insurers} />
          <MultiSelect name="payers" label="Insurers you bill" placeholder="Pick several" data={insurers} defaultValue={["sandbox", "xceli-a"]} />
          <RemoteSelect name="item" source="items" label="Standard item" placeholder="Type to search, e.g. malaria" />
          <RemoteMultiSelect name="diagnoses" source="diagnoses" label="Diagnoses (ICD-10)" placeholder="Code or words, e.g. B54" />
          <TagsInput name="tags" label="Tags" placeholder="Type and press Enter" defaultValue={["outpatient", "October"]} />
          <SegmentedControl
            name="env"
            data={[
              { value: "sandbox", label: "Sandbox" },
              { value: "live", label: "Live" },
            ]}
          />
        </Section>

        <Section title="Dates" note="Typed in day-first order or picked; sent as ISO dates.">
          <DateInput name="encounter" label="Encounter date" placeholder="20 Oct 2026" />
          <DateRangeInput name="period" label="Billing period" placeholder="First and last day" />
          <DateTimeInput name="admitted" label="Admitted at" placeholder="Day and time" />
        </Section>

        <Section title="Text areas" note="Plain text grows with content; rich text sends HTML.">
          <Textarea name="note" label="Note to the insurer" placeholder="Plain text" />
          <div className="ph-gallery__wide">
            <RichTextArea
              name="body"
              label="Clinical summary"
              description="Bold, lists and links are kept"
              placeholder="Presenting complaint, findings, plan…"
              defaultValue="<p>Fever for <strong>three days</strong>; RDT positive.</p><ul><li>Artemether-lumefantrine</li></ul>"
            />
          </div>
        </Section>

        <Section title="Choices" note="Native checkboxes and switches, themed.">
          <Checkbox name="consent" value="yes" label="The member consented to sharing this claim" />
          <Switch name="auto_submit" value="on" label="Send claims as soon as they pass checks" defaultChecked />
        </Section>

        <div className="ph-gallery__foot">
          <button type="submit" className="ph-button">
            Show what the form sends
          </button>
        </div>
        {sent && (
          <pre className="ph-gallery__sent" aria-live="polite">
            {sent.map(([k, v]) => `${k} = ${v}`).join("\n")}
          </pre>
        )}
      </form>
    </UIProvider>
  );
}
