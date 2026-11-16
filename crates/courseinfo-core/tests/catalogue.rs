use std::collections::BTreeMap;

use courseinfo_core::{
    Course, Error, course_department, normalize_course, parse_department, parse_semester,
    semester_for_timestamp, serialize_department,
};
use serde::Deserialize;

const COMP: &str = include_str!("../../../tests/fixtures/2610-comp.html");
const ELEC: &str = include_str!("../../../tests/fixtures/2610-elec.html");
const IMBA: &str = include_str!("../../../tests/fixtures/2610-imba.html");

#[derive(Deserialize)]
struct Expected {
    semester: String,
    department: String,
    departments: Vec<String>,
    courses: BTreeMap<String, Course>,
}

#[test]
fn independently_extracted_source_expectations_match_complete_snapshots() {
    for (html, json) in [
        (
            COMP,
            include_str!("../../../tests/fixtures/2610-comp.expected.json"),
        ),
        (
            ELEC,
            include_str!("../../../tests/fixtures/2610-elec.expected.json"),
        ),
        (
            IMBA,
            include_str!("../../../tests/fixtures/2610-imba.expected.json"),
        ),
    ] {
        let expected: Expected = serde_json::from_str(json).unwrap();
        let actual = parse_department(html, &expected.semester, &expected.department).unwrap();
        assert_eq!(actual.courses, expected.courses);
        assert_eq!(actual.departments, expected.departments);
        assert_eq!(actual.departments.len(), 100);
    }
}

#[test]
fn real_tutorial_tas_replace_instructors_and_repeated_names_retain_source_order() {
    let page = parse_department(COMP, "2610", "COMP").unwrap();
    let tutorial = &page.courses["COMP4900"];
    assert_eq!(tutorial.sections.as_ref().unwrap(), &["T1", "T2", "T3"]);
    assert_eq!(tutorial.instructors["HAGHIGHI, Arman"], ["T1", "T2", "T3"]);
    assert_eq!(tutorial.instructors["WAN, Him Ting"], ["T1", "T2", "T3"]);
    assert!(!tutorial.instructors.contains_key("LEUNG, Wai Ting"));
    assert_eq!(
        page.courses["COMP1021"].instructors["USMAN, Muhammad"],
        ["LA1", "LA2", "LA3", "LA4"]
    );
    assert!(page.courses["COMP7990"].instructors.is_empty());
}

#[test]
fn only_final_credits_suffix_is_removed_and_fractional_credits_are_preserved() {
    let elec = parse_department(ELEC, "2610", "ELEC").unwrap();
    assert_eq!(
        elec.courses["ELEC2991"].title,
        "Industrial Experience (Electronic Engineering)"
    );
    assert_eq!(
        elec.courses["ELEC5010"].title,
        "Introduction to the Design & Implementation of Micro-Systems"
    );
    let encoded = ELEC.replace("Design & Implementation", "Design &amp; Implementation");
    assert_eq!(
        parse_department(&encoded, "2610", "ELEC").unwrap().courses,
        elec.courses
    );
    assert_eq!(
        parse_department(IMBA, "2610", "IMBA").unwrap().courses["IMBA5620"].credits,
        0.5
    );
}

#[test]
fn inline_markup_preserves_contiguous_text_and_actual_whitespace() {
    let expected: Expected = serde_json::from_str(include_str!(
        "../../../tests/fixtures/2610-comp.expected.json"
    ))
    .unwrap();
    for html in [
        COMP.replace("Computer Science", "Computer <em>Sci</em>ence"),
        COMP.replace("(3 units)", "(<span>3</span> units)"),
        COMP.replace(">LAM, Gibson</a>", ">L<em>AM</em>, Gibson</a>"),
        COMP.replace(
            ">LAM, Gibson</a>",
            ">\n L<em>AM</em>,<span>\t&nbsp; </span>Gibson \n</a>",
        ),
    ] {
        let page = parse_department(&html, "2610", "COMP").unwrap();
        assert_eq!(page.courses, expected.courses);
    }
}

#[test]
fn compact_html_cannot_expand_into_unbounded_section_associations() {
    // Under 128 KiB of HTML would otherwise allocate over 32 MiB of copied
    // section identifiers, before the Worker's serialized-snapshot check.
    let section = "L".repeat(65_536);
    let anchors = format!(">I</a>{}", "<a>I</a>".repeat(511));
    let html = COMP
        .replacen("L1 (2225)", &format!("{section} (2225)"), 1)
        .replacen(">LAM, Gibson</a>", &anchors, 1);
    assert!(html.len() < 128 * 1024);
    assert!(matches!(
        parse_department(&html, "2610", "COMP"),
        Err(Error::ParsedOutputTooLarge)
    ));
}

#[test]
fn parsed_output_budget_is_shared_across_courses() {
    let lecture = "L".repeat(65_536);
    let tutorial = "T".repeat(65_536);
    let anchors = format!(">I</a>{}", "<a>I</a>".repeat(159));
    let lecture_page =
        COMP.replacen("L1 (", &format!("{lecture} ("), 1)
            .replacen(">LAM, Gibson</a>", &anchors, 1);
    let tutorial_page = COMP.replacen("T1 (", &format!("{tutorial} ("), 1).replacen(
        ">HAGHIGHI, Arman</a>",
        &anchors,
        1,
    );
    for (html, code, section) in [
        (&lecture_page, "COMP1021", &lecture),
        (&tutorial_page, "COMP4900", &tutorial),
    ] {
        let page = parse_department(html, "2610", "COMP").unwrap();
        assert_eq!(&page.courses[code].sections.as_ref().unwrap()[0], section);
    }
    let combined = lecture_page
        .replacen("T1 (", &format!("{tutorial} ("), 1)
        .replacen(">HAGHIGHI, Arman</a>", &anchors, 1);
    assert!(matches!(
        parse_department(&combined, "2610", "COMP"),
        Err(Error::ParsedOutputTooLarge)
    ));
}

#[test]
fn snapshot_byte_limit_counts_json_escaping_and_preserves_utf8() {
    let mut expected: Expected = serde_json::from_str(include_str!(
        "../../../tests/fixtures/2610-imba.expected.json"
    ))
    .unwrap();
    expected.courses.get_mut("IMBA5620").unwrap().title = "\"".repeat(1_000_000);
    assert!(matches!(
        serialize_department(&expected.courses),
        Err(Error::SnapshotTooLarge)
    ));

    let title = "é".repeat(500_000);
    expected.courses.get_mut("IMBA5620").unwrap().title = title.clone();
    let encoded = serialize_department(&expected.courses).unwrap();
    assert!(encoded.len() <= 1_800_000);
    let decoded: BTreeMap<String, Course> = serde_json::from_str(&encoded).unwrap();
    assert_eq!(decoded["IMBA5620"].title, title);
    assert_eq!(decoded["IMBA5620"].credits, 0.5);
}

#[test]
fn original_mobile_nested_tables_do_not_duplicate_sections_or_headings() {
    let mobile = include_str!("../../../tests/fixtures/2610-imba5620-mobile-row.html");
    let source = include_str!("../../../tests/fixtures/2610-dbap-malformed.html");
    let start = source.find("<tr class=\"mobileInstructorRow\">").unwrap();
    let end = start + source[start..].find("</tr>").unwrap() + 5;
    let mobile_instructor = &source[start..end];
    let desktop_end = IMBA.find("</tr>").unwrap();
    let html = format!(
        "{}{}{}{}",
        &IMBA[..desktop_end + 5],
        mobile,
        mobile_instructor,
        &IMBA[desktop_end + 5..]
    );
    assert_eq!(
        parse_department(&html, "2610", "IMBA").unwrap().courses,
        parse_department(IMBA, "2610", "IMBA").unwrap().courses
    );
}

#[test]
fn known_course_with_header_only_table_serializes_null_sections() {
    // Structural mutation: no naturally sectionless course was observed. See fixture provenance.
    let start = COMP.rfind("<tr class=\"newsect").unwrap();
    let end = start + COMP[start..].find("</tr>").unwrap() + 5;
    let html = format!("{}{}", &COMP[..start], &COMP[end..]);
    let page = parse_department(&html, "2610", "COMP").unwrap();
    let course = &page.courses["COMP7990"];
    assert_eq!(course.sections, None);
    assert!(course.instructors.is_empty());
    assert_eq!(
        serde_json::to_value(course).unwrap()["sections"],
        serde_json::Value::Null
    );
}

#[test]
fn canonical_course_identifiers_reject_paths_punctuation_whitespace_and_unicode() {
    for (source, normalized, department) in [
        ("comp1021", "COMP1021", "COMP"),
        ("uRoP1000a", "UROP1000A", "UROP"),
        ("AB1xy", "AB1XY", "AB"),
    ] {
        let actual = normalize_course(source).unwrap();
        assert_eq!(actual, normalized);
        assert_eq!(course_department(&actual).unwrap(), department);
    }
    for invalid in [
        "",
        "COMP",
        "1021COMP",
        " COMP1021",
        "COMP 1021",
        "COMP1021/",
        "../COMP1021",
        "COMP1021?x",
        "COMP10A21",
        "CÖMP1021",
        "ＣＯＭＰ1021",
        "COMP1021\n",
    ] {
        assert!(normalize_course(invalid).is_err(), "{invalid:?}");
    }
    assert!(course_department("comp1021").is_err());
}

#[test]
fn semester_decoding_is_canonical_and_handles_padded_years() {
    for (code, season, year) in [
        ("2610", "Fall", "2026"),
        ("2620", "Winter", "2026"),
        ("2630", "Spring", "2027"),
        ("2640", "Summer", "2027"),
    ] {
        let semester = parse_semester(code).unwrap();
        assert_eq!(semester.code, code);
        assert_eq!(semester.name, format!("2026 - 2027 {season}"));
        assert_eq!(semester.year, year);
        assert_eq!(semester.cohort, "2026 - 2027");
    }
    assert_eq!(parse_semester("0030").unwrap().year, "2001");
    assert_eq!(parse_semester("9910").unwrap().cohort, "2099 - 2100");
    for invalid in [
        "",
        "10",
        "610",
        "02610",
        "2611",
        "2600",
        "2650",
        "-110",
        "２６１０",
        "2610\n",
        " 2610",
        "ab10",
    ] {
        assert!(parse_semester(invalid).is_err(), "{invalid:?}");
    }
}

#[test]
fn supplied_clock_resolves_exact_hong_kong_term_boundaries() {
    for (boundary, before, after) in [
        (1_767_196_800_000, "2510", "2520"), // 2026-01-01 00:00 +08
        (1_769_875_200_000, "2520", "2530"), // 2026-02-01 00:00 +08
        (1_780_243_200_000, "2530", "2540"), // 2026-06-01 00:00 +08
        (1_788_192_000_000, "2540", "2610"), // 2026-09-01 00:00 +08
    ] {
        assert_eq!(semester_for_timestamp(boundary - 1).unwrap(), before);
        assert_eq!(semester_for_timestamp(boundary).unwrap(), after);
        assert_eq!(semester_for_timestamp(boundary + 1).unwrap(), after);
    }
    for timestamp in [
        i64::MIN,
        i64::MAX,
        -1,
        0,
        946_656_000_000,
        4_102_416_000_000,
    ] {
        assert!(semester_for_timestamp(timestamp).is_err(), "{timestamp}");
    }
}

#[test]
fn wrong_identity_term_and_missing_selectors_never_become_empty_success() {
    assert!(parse_department(COMP, "2530", "COMP").is_err());
    assert!(parse_department(COMP, "2610", "ELEC").is_err());
    assert!(parse_department(COMP, "2610", "comp").is_err());
    assert!(parse_department(COMP, "2611", "COMP").is_err());
    for html in [
        COMP.replace("<title>COMP -", "<title>Login -"),
        COMP.replace("2026-27 Fall", "2025-26 Spring"),
        COMP.replace("id=\"classes\"", "id=\"challenge\""),
        COMP.replace("class=\"course\"", "class=\"challenge\""),
        COMP.replace("class='subject'", "class='challenge'"),
        COMP.replace("id=\"subjectItems\"", "id=\"challenge\""),
        COMP.replace("class=\"diffMsg\"", "class=\"challenge\""),
    ] {
        assert!(parse_department(&html, "2610", "COMP").is_err());
    }
    let start = COMP.find("<div id=\"classes\">").unwrap() + "<div id=\"classes\">".len();
    let end = COMP.find("<div class=\"diffMsg\"").unwrap();
    let empty = format!("{}{}", &COMP[..start], &COMP[end..]);
    assert!(parse_department(&empty, "2610", "COMP").is_err());
}

#[test]
fn truncated_original_bodies_fail_even_at_complete_course_boundaries() {
    let second_course = COMP
        .match_indices("<div class=\"course\">")
        .nth(1)
        .unwrap()
        .0;
    let header_end = COMP
        .find("Introduction to Computer Science (3 units)</div>")
        .unwrap()
        + "Introduction to Computer Science (3 units)</div>".len();
    let footer = COMP.find("<div class=\"diffMsg\"").unwrap();
    let terminal = COMP.find("<script src=\"/wcq/cgi-bin/msg.php").unwrap();
    for position in [
        second_course,
        header_end,
        footer,
        terminal,
        COMP.trim_end().len() - 7,
    ] {
        assert!(
            parse_department(&COMP[..position], "2610", "COMP").is_err(),
            "truncation at {position}"
        );
    }
}

#[test]
fn one_malformed_course_prevents_publishing_every_course_in_the_department() {
    for html in [
        COMP.replace(
            "Doctoral Thesis Research (0 units)",
            "Doctoral Thesis Research (many units)",
        ),
        COMP.replace(
            "Doctoral Thesis Research (0 units)",
            "Doctoral Thesis Research (NaN units)",
        ),
        COMP.replace(
            "Doctoral Thesis Research (0 units)",
            "Doctoral Thesis Research (0 units) trailing",
        ),
        COMP.replace("COMP 7990 -", "ELEC 7990 -"),
        COMP.replace("name=\"COMP7990\"", "name=\"COMP7991\""),
        COMP.replace("R1 (1182)", " (1182)"),
        COMP.replace("R1 (1182)", "R1 (invalid)"),
        COMP.replace("newsect secteven mainRow", "secteven mainRow"),
        COMP.replace("newsect secteven mainRow", "newsect secteven"),
        COMP.replace("newsect secteven mainRow", "secteven"),
        COMP.replace("newsect secteven mainRow", "mobileViewDetail"),
        COMP.replace("newsect secteven mainRow", "mobileInstructorRow"),
        COMP.replacen("<td >L1 (2225)</td>", "", 1),
    ] {
        assert!(parse_department(&html, "2610", "COMP").is_err());
    }
    let source = include_str!("../../../tests/fixtures/2610-dbap-malformed.html");
    assert!(parse_department(source, "2610", "DBAP").is_err());
}

#[test]
fn duplicate_course_and_section_codes_are_not_silently_overwritten() {
    let first = COMP.find("<div class=\"course\">").unwrap();
    let second = COMP
        .match_indices("<div class=\"course\">")
        .nth(1)
        .unwrap()
        .0;
    let duplicate = format!(
        "{}{}{}",
        &COMP[..second],
        &COMP[first..second],
        &COMP[second..]
    );
    assert!(parse_department(&duplicate, "2610", "COMP").is_err());
    let duplicate = COMP.replace("L2 (2227)", "L1 (2227)");
    assert!(parse_department(&duplicate, "2610", "COMP").is_err());
}

#[test]
fn discovery_deduplicates_ug_pg_but_never_accepts_arbitrary_urls() {
    let link = "<a class=\"ug\" href=\"/wcq/cgi-bin/2610/subject/COMP\">COMP</a>";
    let duplicate = format!("{link}{}", link.replace("class=\"ug\"", "class=\"pg\""));
    let page = parse_department(&COMP.replace(link, &duplicate), "2610", "COMP").unwrap();
    assert_eq!(
        page.departments
            .iter()
            .filter(|d| d.as_str() == "COMP")
            .count(),
        1
    );
    let path = "/wcq/cgi-bin/2610/subject/COMP";
    let absolute = COMP.replace(path, &format!("https://w5.ab.ust.hk{path}"));
    assert_eq!(
        parse_department(&absolute, "2610", "COMP")
            .unwrap()
            .departments,
        page.departments
    );
    for bad in [
        "https://evil.example/wcq/cgi-bin/2610/subject/COMP",
        "https://w5.ab.ust.hk.evil.example/wcq/cgi-bin/2610/subject/COMP",
        "https://w5.ab.ust.hk@evil.example/wcq/cgi-bin/2610/subject/COMP",
        "http://w5.ab.ust.hk/wcq/cgi-bin/2610/subject/COMP",
        "//w5.ab.ust.hk/wcq/cgi-bin/2610/subject/COMP",
        "/wcq/cgi-bin/2530/subject/COMP",
        "/wcq/cgi-bin/2610/subject/ELEC",
        "/wcq/cgi-bin/2610/subject/COMP?redirect=evil",
        "/wcq/cgi-bin/2610/subject/COMP/../ELEC",
        "/wcq/cgi-bin/2610/subject/COMP#fragment",
    ] {
        assert!(
            parse_department(&COMP.replace(path, bad), "2610", "COMP").is_err(),
            "{bad}"
        );
    }
}
