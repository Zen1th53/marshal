# Contributor IP Onboarding Guide

This guide walks new contributors through the step-by-step process of fulfilling MARSHAL intellectual property requirements before submitting pull requests.

---

## Step 1: Identify Your Contribution Type

Before opening a pull request, determine whether your contribution is **Individual** or **Corporate**:

### Case A: Individual Contributor
You write code on your own personal time, using personal equipment, and you own 100% of the copyright in your work under applicable law.

* **Required Form**: [`legal/INDIVIDUAL-CONTRIBUTOR-ASSIGNMENT.md`](../../legal/INDIVIDUAL-CONTRIBUTOR-ASSIGNMENT.md) (ICAA)

### Case B: Corporate Contributor
You write code during working hours, using employer-provided equipment, or under an employment/contractor agreement where your employer or client automatically owns the IP created by you.

* **Required Form**: [`legal/CORPORATE-CONTRIBUTOR-ASSIGNMENT.md`](../../legal/CORPORATE-CONTRIBUTOR-ASSIGNMENT.md) (CCAA)
* **Required Signer**: An authorized corporate officer (VP of Eng, General Counsel, CISO, or CTO) who has legal authority to bind your company.

---

## Step 2: Execute and Submit Agreement

1. Download and fill out the appropriate draft agreement.
2. Sign the document (digital or physical signature).
3. Email the executed document to `extreme29@proton.me` with the subject:
   `[MARSHAL Contributor Agreement] - @your-github-username`
4. Do **not** commit personal private information (physical addresses, phone numbers, Tax IDs) into public Git commits or PR comments.

---

## Step 3: Registry Registration & Verification

1. The MARSHAL maintainer will verify the document and archive it in private legal records.
2. The maintainer will update [`legal/assignment-registry.yml`](../../legal/assignment-registry.yml) with an entry referencing your GitHub username and private `record_ref`.
3. Future Pull Requests submitted by your GitHub account will automatically pass the automated `Contributor Rights Check` CI gate.

---

## Step 4: PR Disclosure Guidelines

When opening a Pull Request, disclose:

* [ ] Any third-party open-source code included or adapted (see [`docs/legal/THIRD-PARTY-POLICY.md`](THIRD-PARTY-POLICY.md)).
* [ ] Any AI-generated code produced by external models (see [`docs/legal/AI-CONTRIBUTION-POLICY.md`](AI-CONTRIBUTION-POLICY.md)).
