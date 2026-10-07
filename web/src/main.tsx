import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "./App";
import "./index.css";
import { initializePreferences } from "./lib/preferences";
initializePreferences();
const client = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: true, retry: false } },
});
createRoot(document.getElementById("root")!).render(
  <QueryClientProvider client={client}>
    <App />
  </QueryClientProvider>,
);
