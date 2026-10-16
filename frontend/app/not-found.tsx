import Link from "next/link";

export default function NotFound() {
  return (
    <main className="grid min-h-screen place-items-center p-6">
      <div className="max-w-sm">
        <h1 className="text-2xl font-semibold">That page isn't here</h1>
        <p className="mt-2 text-ink-soft">The link may be mistyped. Boards live at /board followed by a code.</p>
        <Link href="/" className="mt-5 inline-flex h-11 items-center rounded-lg bg-ink px-5 font-medium text-board">
          Back to start
        </Link>
      </div>
    </main>
  );
}
