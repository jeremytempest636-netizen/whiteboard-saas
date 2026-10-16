import type { ReactNode, SVGProps } from "react";

function Svg({ children, ...rest }: SVGProps<SVGSVGElement> & { children: ReactNode }) {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 20 20"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      {...rest}
    >
      {children}
    </svg>
  );
}

export const SelectIcon = () => (
  <Svg>
    <path d="M5 3.5 16.5 10l-5.3 1.7L9 17z" />
  </Svg>
);
export const PenIcon = () => (
  <Svg>
    <path d="m4 16 .9-3.6L13.6 3.7a1.7 1.7 0 0 1 2.4 2.4L7.4 14.9z" />
    <path d="m12 5.4 2.6 2.6" />
  </Svg>
);
export const RectIcon = () => (
  <Svg>
    <rect x="3.5" y="5" width="13" height="10" rx="1.4" />
  </Svg>
);
export const EllipseIcon = () => (
  <Svg>
    <ellipse cx="10" cy="10" rx="7" ry="5.4" />
  </Svg>
);
export const ArrowIcon = () => (
  <Svg>
    <path d="M4 16 16 4M8.5 4H16v7.5" />
  </Svg>
);
export const TextIcon = () => (
  <Svg>
    <path d="M5 5.5V4h10v1.5M10 4v12M8 16h4" />
  </Svg>
);
export const EraserIcon = () => (
  <Svg>
    <path d="m8 16.5-3.6-3.6a1.5 1.5 0 0 1 0-2.1l6-6a1.5 1.5 0 0 1 2.1 0l3.6 3.6a1.5 1.5 0 0 1 0 2.1L9.5 16.5H16" />
    <path d="m7.5 8.2 5.3 5.3" />
  </Svg>
);
export const HandIcon = () => (
  <Svg>
    <path d="M10 2.8v14.4M2.8 10h14.4M10 2.8 8.2 4.6M10 2.8l1.8 1.8M10 17.2l-1.8-1.8M10 17.2l1.8-1.8M2.8 10l1.8-1.8M2.8 10l1.8 1.8M17.2 10l-1.8-1.8M17.2 10l-1.8 1.8" />
  </Svg>
);
export const UndoIcon = () => (
  <Svg>
    <path d="M7.5 5.5 4 9l3.5 3.5M4.5 9H12a4 4 0 0 1 0 8h-1.5" />
  </Svg>
);
export const RedoIcon = () => (
  <Svg>
    <path d="M12.5 5.5 16 9l-3.5 3.5M15.5 9H8a4 4 0 0 0 0 8h1.5" />
  </Svg>
);
export const TrashIcon = () => (
  <Svg>
    <path d="M4 6h12M8 6V4.2h4V6M5.7 6l.7 10h7.2l.7-10M8.5 9v4.5M11.5 9v4.5" />
  </Svg>
);
export const DownloadIcon = () => (
  <Svg>
    <path d="M10 3.5v9M6.5 9.5 10 13l3.5-3.5M4 16h12" />
  </Svg>
);
export const LinkIcon = () => (
  <Svg>
    <path d="M8.6 11.4a3 3 0 0 0 4.2 0l2.3-2.3a3 3 0 0 0-4.2-4.2l-.9.9M11.4 8.6a3 3 0 0 0-4.2 0l-2.3 2.3a3 3 0 0 0 4.2 4.2l.9-.9" />
  </Svg>
);
export const PlusIcon = () => (
  <Svg>
    <path d="M10 4.5v11M4.5 10h11" />
  </Svg>
);
export const MinusIcon = () => (
  <Svg>
    <path d="M4.5 10h11" />
  </Svg>
);
export const FitIcon = () => (
  <Svg>
    <path d="M4 7.5V4h3.5M16 7.5V4h-3.5M4 12.5V16h3.5M16 12.5V16h-3.5" />
  </Svg>
);
