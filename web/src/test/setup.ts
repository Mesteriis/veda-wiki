import { configure } from "@testing-library/react";

// Component tests wait for rendered state with findBy/waitFor. The library's
// one-second default is too tight when the machine is busy (a parallel Go race
// run made AccessSettings tests fail intermittently), so allow five seconds.
configure({ asyncUtilTimeout: 5000 });
