# Marshal v0.4.2

A fix for architecture submission, found in a real project.

## Fixed

- **Architecture submission no longer loops on "the project changed".**
  A draft that left `contract.project` out was refused as a project change,
  so the Marshal re-read the context and resubmitted again and again, and the
  architecture never reached the browser. A draft without `contract.project`
  is now bound to the project it was sent from. A draft naming another
  project is refused with the exact value it must carry.
