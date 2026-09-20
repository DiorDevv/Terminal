import { describe, expect, it } from "vitest";
import { keywordToPattern } from "./access";

// squid matches url_regex with POSIX extended regular expressions; JavaScript's
// syntax agrees on everything used here, so it can check the pattern's meaning.
const toRegExp = (word: string) => new RegExp(keywordToPattern(word), "i");

describe("keywordToPattern", () => {
  it("turns a plain word into a pattern that matches that word", () => {
    expect(toRegExp("casino").test("http://x.example/CASINO-bonus")).toBe(true);
    expect(toRegExp("casino").test("http://x.example/cinema")).toBe(false);
  });

  it("a dot means a dot, not any character", () => {
    expect(toRegExp("bet.com").test("http://bet.com/")).toBe(true);
    expect(toRegExp("bet.com").test("http://betXcom/")).toBe(false);
  });

  it("every regex metacharacter in a keyword is taken literally", () => {
    for (const word of ["c++", "a(b", "x[y]", "1+1=2", "price$", "^start", "a|b", "what?", "a*b", "{n}", "back\\slash"]) {
      const pattern = keywordToPattern(word);
      expect(() => new RegExp(pattern), `${word} -> ${pattern}`).not.toThrow();
      expect(new RegExp(pattern).test(`http://x.example/${word}/page`), word).toBe(true);
    }
    // ...and they must not match things the plain text does not contain
    expect(toRegExp("a|b").test("http://x.example/a")).toBe(false);
    expect(toRegExp("c++").test("http://x.example/ccc")).toBe(false);
    expect(toRegExp("a*b").test("http://x.example/aab")).toBe(false);
  });
});
