"use client";
import {useState} from "react";
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";

// The public aggregate cache contains no session-owned state. A boot-time 401
// intentionally clears the app's private cache; it must not cancel this page.
export default function LiveLayout({children}:{children:React.ReactNode}){
 const [client]=useState(()=>new QueryClient());
 return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}
