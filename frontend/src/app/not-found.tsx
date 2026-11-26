import Link from "next/link";

export default function NotFound() {
  return (
    <main className="grid min-h-dvh place-items-center p-6 text-center">
      <div className="space-y-3">
        <p className="text-sm font-semibold text-primary">404</p>
        <h1 className="text-2xl font-bold">Page not found</h1>
        <Link href="/dashboard" className="text-sm text-primary underline-offset-4 hover:underline">
          Back to dashboard
        </Link>
      </div>
    </main>
  );
}
