import type { Metadata, Viewport } from "next";
import "@fontsource-variable/familjen-grotesk";
import "@fontsource/caveat-brush";
import "./globals.css";

export const metadata: Metadata = {
  title: "Whiteboard — draw together, live",
  description: "A shared whiteboard where everyone's pen shows up in real time. Open a board, send the link, start drawing.",
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  themeColor: "#cfd5d1",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
