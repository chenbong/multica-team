import {afterEach,expect,it} from "vitest";
import {cleanup,render,screen} from "@testing-library/react";
import {AnimatedNumber} from "./animated-number";
import {readFileSync} from "node:fs";
afterEach(cleanup);
it("renders the initial value without inventing an initial zero or animation",()=>{
 const {container}=render(<AnimatedNumber value={1268}/>);
 expect(screen.getByText("1,268")).toBeInTheDocument();expect(container.querySelector(".tm-live-numberChanging")).toBeNull();
});
it("slides a changed value in the correct direction and hides the outgoing value from assistive tools",()=>{
 const {container,rerender}=render(<AnimatedNumber value={12}/>);rerender(<AnimatedNumber value={18}/>);
 expect(container.querySelector(".tm-live-numberChanging")).toHaveAttribute("data-direction","up");
 expect(screen.getByText("12")).toHaveAttribute("aria-hidden","true");expect(screen.getByText("18")).not.toHaveAttribute("aria-hidden");
 rerender(<AnimatedNumber value={9}/>);expect(container.querySelector(".tm-live-numberChanging")).toHaveAttribute("data-direction","down");expect(container.querySelector(".tm-live-numberNew")).toHaveTextContent("9");
});
it("does not replay animation on an unchanged polling result",()=>{
 const {container,rerender}=render(<AnimatedNumber value={1}/>);rerender(<AnimatedNumber value={2}/>);const stack=container.querySelector(".tm-live-numberStack");
 rerender(<AnimatedNumber value={2}/>);expect(container.querySelector(".tm-live-numberStack")).toBe(stack);
});
it("shows unavailable or first recovered data immediately without counting from a fabricated zero",()=>{
 const {container,rerender}=render(<AnimatedNumber value={null}/>);expect(screen.getByText("—")).toBeInTheDocument();
 rerender(<AnimatedNumber value={0}/>);expect(screen.getByText("0")).toBeInTheDocument();expect(container.querySelector(".tm-live-numberChanging")).toBeNull();
 rerender(<AnimatedNumber value={null}/>);expect(container.querySelector(".tm-live-numberOld")).toBeNull();
 rerender(<AnimatedNumber value={24}/>);expect(screen.getByText("24")).toBeInTheDocument();expect(container.querySelector(".tm-live-numberChanging")).toBeNull();
});
it("replaces an interrupted transition with the newest real target",()=>{
 const {container,rerender}=render(<AnimatedNumber value={1}/>);rerender(<AnimatedNumber value={2}/>);rerender(<AnimatedNumber value={3}/>);
 expect(container.querySelector(".tm-live-numberOld")).toHaveTextContent("2");expect(container.querySelector(".tm-live-numberNew")).toHaveTextContent("3");expect(screen.queryByText("1")).not.toBeInTheDocument();
});
it("disables movement and hides outgoing text when reduced motion is requested",()=>{
 const css=readFileSync("app/live/preview.css","utf8");
 expect(css).toContain("@media(prefers-reduced-motion:reduce)");expect(css).toContain(".tm-live-numberNew{animation:none}");expect(css).toContain(".tm-live-numberStack>.tm-live-numberOld{display:none;animation:none}");
});
