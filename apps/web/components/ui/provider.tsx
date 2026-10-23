"use client";

// The form kit's theme. Mantine supplies the behaviour (keyboard, focus,
// popovers, ARIA); the theme maps it onto the console's look: 8px corners,
// the Ink text, the Indigo accent. Only the stylesheets of the parts we use
// are loaded, and none of Mantine's page baseline, so globals.css still owns
// the page.
import "@mantine/core/styles/default-css-variables.css";
import "@mantine/core/styles/global.css";
import "@mantine/core/styles/UnstyledButton.css";
import "@mantine/core/styles/VisuallyHidden.css";
import "@mantine/core/styles/Input.css";
import "@mantine/core/styles/InlineInput.css";
import "@mantine/core/styles/Popover.css";
import "@mantine/core/styles/Combobox.css";
import "@mantine/core/styles/ScrollArea.css";
import "@mantine/core/styles/Pill.css";
import "@mantine/core/styles/PillsInput.css";
import "@mantine/core/styles/CloseButton.css";
import "@mantine/core/styles/ActionIcon.css";
import "@mantine/core/styles/Loader.css";
import "@mantine/core/styles/NumberInput.css";
import "@mantine/core/styles/PasswordInput.css";
import "@mantine/core/styles/Checkbox.css";
import "@mantine/core/styles/CheckboxIndicator.css";
import "@mantine/core/styles/Radio.css";
import "@mantine/core/styles/RadioIndicator.css";
import "@mantine/core/styles/Switch.css";
import "@mantine/core/styles/SegmentedControl.css";
import "@mantine/core/styles/Tooltip.css";
import "@mantine/core/styles/Group.css";
import "@mantine/dates/styles.css";
import "@mantine/tiptap/styles.css";
import "./ui.css";

import { createTheme, MantineProvider, type MantineColorsTuple } from "@mantine/core";
import { DatesProvider } from "@mantine/dates";
import type { ReactNode } from "react";

// Indigo #5A5CF0 at shade 6, from the Ananse exchange palette.
const indigo: MantineColorsTuple = [
  "#F1F0FE",
  "#E2E2FD",
  "#C4C5FB",
  "#A5A7F8",
  "#8C8EFF",
  "#7072F3",
  "#5A5CF0",
  "#4648D8",
  "#3738B4",
  "#292A8C",
];

const theme = createTheme({
  primaryColor: "indigo",
  primaryShade: 6,
  colors: { indigo },
  black: "#1F0F3A",
  fontFamily: "var(--font)",
  fontFamilyMonospace: "var(--font-mono), ui-monospace, monospace",
  defaultRadius: "md",
  radius: { xs: "4px", sm: "6px", md: "8px", lg: "12px", xl: "16px" },
  cursorType: "pointer",
  focusRing: "auto",
  respectReducedMotion: true,
  components: {
    Input: { defaultProps: { size: "sm" } },
    Combobox: { defaultProps: { shadow: "md", transitionProps: { transition: "pop", duration: 120 } } },
    Popover: { defaultProps: { shadow: "md" } },
  },
});

export function UIProvider({ children }: { children: ReactNode }) {
  return (
    <MantineProvider theme={theme} forceColorScheme="light">
      <DatesProvider settings={{ locale: "en", firstDayOfWeek: 1, consistentWeeks: true }}>{children}</DatesProvider>
    </MantineProvider>
  );
}
