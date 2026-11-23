//! Pure catalogue parsing, term resolution, and resource-bounded snapshot encoding.

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;
use std::io::{self, Write};
use std::mem::size_of;
use std::sync::LazyLock;

use scraper::{ElementRef, Html, Selector};
use serde::{Deserialize, Serialize};

const MAX_PARSED_OUTPUT_BYTES: usize = 16 * 1024 * 1024;
pub const MAX_SNAPSHOT_BYTES: usize = 1_800_000;

#[derive(Serialize, Deserialize, Clone, Debug, PartialEq)]
pub struct Course {
    pub code: String,
    pub title: String,
    pub credits: f64,
    pub instructors: BTreeMap<String, Vec<String>>,
    pub sections: Option<Vec<String>>,
}

#[derive(Serialize, Deserialize, Debug, PartialEq, Eq)]
pub struct Semester {
    pub code: String,
    pub name: String,
    pub year: String,
    pub cohort: String,
}

#[derive(Debug)]
pub struct DepartmentPage {
    pub courses: BTreeMap<String, Course>,
    pub departments: Vec<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Error {
    InvalidCourse,
    InvalidSemester,
    TimestampOutOfRange,
    InvalidDepartment,
    InvalidPage(String),
    ParsedOutputTooLarge,
    SnapshotTooLarge,
    Serialization(String),
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InvalidCourse => f.write_str("invalid course identifier"),
            Self::InvalidSemester => f.write_str("invalid semester identifier"),
            Self::TimestampOutOfRange => {
                f.write_str("timestamp outside supported academic years 2000–2099")
            }
            Self::InvalidDepartment => f.write_str("invalid department identifier"),
            Self::InvalidPage(message) => write!(f, "invalid HKUST department page: {message}"),
            Self::ParsedOutputTooLarge => {
                f.write_str("department parsed output exceeds allocation budget")
            }
            Self::SnapshotTooLarge => {
                f.write_str("department snapshot exceeds the storage size limit")
            }
            Self::Serialization(message) => write!(f, "snapshot serialization failed: {message}"),
        }
    }
}

impl std::error::Error for Error {}

pub type Result<T, E = Error> = std::result::Result<T, E>;

fn invalid_page(message: impl Into<String>) -> Error {
    Error::InvalidPage(message.into())
}

struct OutputBudget {
    remaining: usize,
}

impl OutputBudget {
    fn charge(&mut self, parts: &[usize]) -> Result<()> {
        self.remaining = parts
            .iter()
            .try_fold(self.remaining, |remaining, bytes| {
                remaining.checked_sub(*bytes)
            })
            .ok_or(Error::ParsedOutputTooLarge)?;
        Ok(())
    }
}

struct SnapshotWriter {
    bytes: Vec<u8>,
}

impl Write for SnapshotWriter {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        if self
            .bytes
            .len()
            .checked_add(bytes.len())
            .is_none_or(|length| length > MAX_SNAPSHOT_BYTES)
        {
            return Err(io::ErrorKind::FileTooLarge.into());
        }
        self.bytes.extend_from_slice(bytes);
        Ok(bytes.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

/// Encode validated courses without first allocating an over-limit JSON string.
pub fn serialize_department(courses: &BTreeMap<String, Course>) -> Result<String> {
    let mut output = SnapshotWriter { bytes: Vec::new() };
    serde_json::to_writer(&mut output, courses).map_err(|error| {
        if error.io_error_kind() == Some(io::ErrorKind::FileTooLarge) {
            Error::SnapshotTooLarge
        } else {
            Error::Serialization(error.to_string())
        }
    })?;
    String::from_utf8(output.bytes).map_err(|error| Error::Serialization(error.to_string()))
}

fn department_end(code: &str) -> Option<usize> {
    let bytes = code.as_bytes();
    let department = bytes.iter().take_while(|b| b.is_ascii_alphabetic()).count();
    let numbers = bytes[department..]
        .iter()
        .take_while(|b| b.is_ascii_digit())
        .count();
    (department > 0
        && numbers > 0
        && bytes[department + numbers..]
            .iter()
            .all(u8::is_ascii_alphabetic))
    .then_some(department)
}

pub fn normalize_course(code: &str) -> Result<String> {
    department_end(code).ok_or(Error::InvalidCourse)?;
    Ok(code.to_ascii_uppercase())
}

pub fn course_department(normalized: &str) -> Result<&str> {
    let end = department_end(normalized).ok_or(Error::InvalidCourse)?;
    if normalized.bytes().any(|b| b.is_ascii_lowercase()) {
        return Err(Error::InvalidCourse);
    }
    Ok(&normalized[..end])
}

pub fn parse_semester(code: &str) -> Result<Semester> {
    if code.len() != 4 || !code.bytes().all(|b| b.is_ascii_digit()) {
        return Err(Error::InvalidSemester);
    }
    let season = match &code[2..] {
        "10" => "Fall",
        "20" => "Winter",
        "30" => "Spring",
        "40" => "Summer",
        _ => return Err(Error::InvalidSemester),
    };
    let start =
        2000 + u16::from(code.as_bytes()[0] - b'0') * 10 + u16::from(code.as_bytes()[1] - b'0');
    let cohort = format!("{start} - {}", start + 1);
    let year = if matches!(&code[2..], "30" | "40") {
        start + 1
    } else {
        start
    };
    Ok(Semester {
        code: code.to_owned(),
        name: format!("{cohort} {season}"),
        year: year.to_string(),
        cohort,
    })
}

/// Resolve a supplied Unix timestamp using UTC+8, without a host timezone or clock.
pub fn semester_for_timestamp(timestamp_ms: i64) -> Result<String> {
    let local = timestamp_ms
        .checked_add(8 * 60 * 60 * 1000)
        .ok_or(Error::TimestampOutOfRange)?;
    let days = local.div_euclid(86_400_000);
    // Gregorian civil date from Unix epoch days (400-year eras, including negative dates).
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let day_of_era = z - era * 146_097;
    let year_of_era =
        (day_of_era - day_of_era / 1460 + day_of_era / 36_524 - day_of_era / 146_096) / 365;
    let mut year = year_of_era + era * 400;
    let day_of_year = day_of_era - (365 * year_of_era + year_of_era / 4 - year_of_era / 100);
    let month_prime = (5 * day_of_year + 2) / 153;
    let month = month_prime + if month_prime < 10 { 3 } else { -9 };
    year += i64::from(month <= 2);
    if !(2000..=2099).contains(&year) {
        return Err(Error::TimestampOutOfRange);
    }
    let (academic_year, suffix) = match month {
        1 => (year - 1, "20"),
        2..=5 => (year - 1, "30"),
        6..=8 => (year - 1, "40"),
        _ => (year, "10"),
    };
    if academic_year < 2000 {
        return Err(Error::TimestampOutOfRange);
    }
    Ok(format!("{:02}{suffix}", academic_year - 2000))
}

fn selector(value: &str) -> Selector {
    Selector::parse(value).expect("static catalogue selector")
}

struct Selectors {
    title: Selector,
    term: Selector,
    departments: Selector,
    links: Selector,
    classes: Selector,
    footer: Selector,
    courses: Selector,
    subjects: Selector,
    course: CourseSelectors,
}

static SELECTORS: LazyLock<Selectors> = LazyLock::new(|| Selectors {
    title: selector("head > title"),
    term: selector(".search-box > .term:not(.classattri) > #dropdownMenu2 > a"),
    departments: selector(".depts > #subjectItems"),
    links: selector("a"),
    classes: selector("body > #classes"),
    footer: selector(":scope > .diffMsg"),
    courses: selector("div.course"),
    subjects: selector("div.subject"),
    course: CourseSelectors {
        header: selector(":scope > .courseinfo > .courseattrContainer > .subject"),
        anchor: selector(":scope > .courseanchor > a[name]"),
        table: selector(":scope > table.sections"),
        headings: selector(":scope > tbody > tr > th, :scope > tr > th"),
        table_rows: selector(":scope > tbody > tr, :scope > tr"),
        rows: selector("tr.newsect"),
        main_rows: selector("tr.mainRow"),
        instructor: selector(":scope > div.instructorList > a"),
        ta: selector(":scope > div.taListContainer > div.taList > a"),
    },
});

fn text(element: ElementRef<'_>) -> String {
    let mut result = String::new();
    let mut whitespace = false;
    for part in element.text() {
        for character in part.chars() {
            if character.is_whitespace() {
                whitespace = true;
            } else {
                if whitespace && !result.is_empty() {
                    result.push(' ');
                }
                result.push(character);
                whitespace = false;
            }
        }
    }
    result
}

fn exactly_one<'a>(
    mut elements: impl Iterator<Item = ElementRef<'a>>,
    label: &str,
) -> Result<ElementRef<'a>> {
    let element = elements
        .next()
        .ok_or_else(|| invalid_page(format!("missing {label}")))?;
    if elements.next().is_some() {
        return Err(invalid_page(format!("duplicate {label}")));
    }
    Ok(element)
}

fn valid_department(value: &str) -> bool {
    !value.is_empty() && value.bytes().all(|b| b.is_ascii_uppercase())
}

const UPDATE_MESSAGE: &str =
    "The class information on this page will normally be updated every 15 to 20 minutes.";

/// Parse one complete department. A single invalid course invalidates the entire snapshot.
pub fn parse_department(html: &str, semester: &str, department: &str) -> Result<DepartmentPage> {
    let term = parse_semester(semester)?;
    if !valid_department(department) {
        return Err(Error::InvalidDepartment);
    }
    // html5ever repairs missing closers. Check the original ending, not the script's source.
    if html
        .trim_end()
        .strip_suffix("</html>")
        .and_then(|s| s.trim_end().strip_suffix("</body>"))
        .and_then(|s| s.trim_end().strip_suffix("</script>"))
        .is_none()
    {
        return Err(invalid_page(
            "missing original terminal script/body/html evidence",
        ));
    }
    let selectors = &*SELECTORS;
    let document = Html::parse_document(html);
    let title = exactly_one(document.select(&selectors.title), "page title")?;
    if text(title) != format!("{department} - HKUST Class Schedule & Quota") {
        return Err(invalid_page("wrong department page identity"));
    }
    let selected_term = exactly_one(document.select(&selectors.term), "selected term")?;
    let start = 2000
        + semester[..2]
            .parse::<u16>()
            .map_err(|_| Error::InvalidSemester)?;
    let season = term.name.rsplit_once(' ').expect("semester season").1;
    let term_label = format!("{start}-{:02} {season}", (start + 1) % 100);
    if text(selected_term) != term_label {
        return Err(invalid_page("wrong selected term"));
    }
    let subject_items = exactly_one(
        document.select(&selectors.departments),
        "department discovery",
    )?;
    let mut budget = OutputBudget {
        remaining: MAX_PARSED_OUTPUT_BYTES,
    };
    let mut departments = BTreeSet::new();
    for link in subject_items.select(&selectors.links) {
        if !link
            .value()
            .classes()
            .any(|class| matches!(class, "ug" | "pg"))
        {
            return Err(invalid_page("department link without UG/PG classification"));
        }
        let code = text(link);
        if !valid_department(&code) {
            return Err(invalid_page("malformed department link text"));
        }
        let href = link
            .value()
            .attr("href")
            .ok_or_else(|| invalid_page("department link without href"))?;
        let expected = format!("/wcq/cgi-bin/{semester}/subject/{code}");
        let path = href.strip_prefix("https://w5.ab.ust.hk").unwrap_or(href);
        if path != expected {
            return Err(invalid_page(
                "department link has unexpected origin, term, or path",
            ));
        }
        budget.charge(&[size_of::<String>(), code.len()])?;
        departments.insert(code);
    }
    if !departments.contains(department) {
        return Err(invalid_page("department absent from discovery"));
    }
    let classes = exactly_one(document.select(&selectors.classes), "course container")?;
    let footer = exactly_one(classes.select(&selectors.footer), "completion message")?;
    if text(footer) != UPDATE_MESSAGE {
        return Err(invalid_page("invalid completion message"));
    }
    let mut courses = BTreeMap::new();
    let mut completed = false;
    for child in classes.children().filter_map(ElementRef::wrap) {
        if child.id() == footer.id() {
            completed = true;
            continue;
        }
        if completed {
            if child.value().name() != "script" {
                return Err(invalid_page("course content after completion message"));
            }
            continue;
        }
        if child.value().name() != "div" || !child.value().classes().any(|c| c == "course") {
            return Err(invalid_page("unexpected element in course container"));
        }
        let course = parse_course(child, department, &selectors.course, &mut budget)?;
        if courses.insert(course.code.clone(), course).is_some() {
            return Err(invalid_page("duplicate course identifier"));
        }
    }
    // Missing selectors are not evidence of an empty department. No real empty marker observed.
    if courses.is_empty() {
        return Err(invalid_page(
            "no verified courses; empty-department marker unobserved",
        ));
    }
    if document.select(&selectors.courses).count() != courses.len()
        || document.select(&selectors.subjects).count() != courses.len()
    {
        return Err(invalid_page(
            "course blocks or headers outside expected structure",
        ));
    }
    Ok(DepartmentPage {
        courses,
        departments: departments.into_iter().collect(),
    })
}

struct CourseSelectors {
    header: Selector,
    anchor: Selector,
    table: Selector,
    headings: Selector,
    table_rows: Selector,
    rows: Selector,
    main_rows: Selector,
    instructor: Selector,
    ta: Selector,
}

fn parse_course(
    element: ElementRef<'_>,
    department: &str,
    selectors: &CourseSelectors,
    budget: &mut OutputBudget,
) -> Result<Course> {
    let header = exactly_one(element.select(&selectors.header), "course header")?;
    let header = text(header);
    let (source_code, title_credits) = header
        .split_once(" - ")
        .ok_or_else(|| invalid_page("course header missing code/title separator"))?;
    let code: String = source_code.split_whitespace().collect();
    let source_department =
        course_department(&code).map_err(|_| invalid_page("malformed course identifier"))?;
    if source_department != department {
        return Err(invalid_page(
            "course does not belong to expected department",
        ));
    }
    let anchor = exactly_one(element.select(&selectors.anchor), "course anchor")?;
    if anchor.value().attr("name") != Some(code.as_str()) {
        return Err(invalid_page("course anchor/header mismatch"));
    }
    let (title, suffix) = title_credits
        .rsplit_once(" (")
        .ok_or_else(|| invalid_page("course has no final credit suffix"))?;
    budget.charge(&[
        size_of::<Course>(),
        size_of::<String>(),
        code.len(),
        code.len(),
        title.len(),
    ])?;
    let units = suffix
        .strip_suffix(')')
        .and_then(|s| s.strip_suffix(" units").or_else(|| s.strip_suffix(" unit")))
        .ok_or_else(|| invalid_page("unrecognized final credit suffix"))?;
    let mut decimal = units.split('.');
    let whole = decimal.next().unwrap_or_default();
    let fractional = decimal.next();
    if title.is_empty()
        || whole.is_empty()
        || !whole.bytes().all(|b| b.is_ascii_digit())
        || fractional.is_some_and(|f| f.is_empty() || !f.bytes().all(|b| b.is_ascii_digit()))
        || decimal.next().is_some()
    {
        return Err(invalid_page("malformed title or numeric credits"));
    }
    let credits: f64 = units.parse().map_err(|_| invalid_page("invalid credits"))?;
    if !credits.is_finite() {
        return Err(invalid_page("non-finite credits"));
    }
    let table = exactly_one(element.select(&selectors.table), "sections table")?;
    let headings = table.select(&selectors.headings).map(text);
    if !headings.eq([
        "Section",
        "Date & Time",
        "Room",
        "Instructor",
        "TA/IA/GTA",
        "Quota",
        "Enrol",
        "Avail",
        "Wait",
        "Remarks",
    ]) {
        return Err(invalid_page("unexpected section table headings"));
    }
    let mut sections = Vec::new();
    let mut instructors: BTreeMap<String, Vec<String>> = BTreeMap::new();
    for row in table.select(&selectors.table_rows) {
        let mut children = row.children().filter_map(ElementRef::wrap);
        let first = children
            .next()
            .ok_or_else(|| invalid_page("empty section table row"))?;
        if first.value().name() == "th" {
            if children.any(|cell| cell.value().name() != "th") {
                return Err(invalid_page("mixed section table header row"));
            }
            continue;
        }
        // The mobile detail row duplicates the desktop section.
        if row
            .value()
            .classes()
            .any(|class| class == "mobileViewDetail")
        {
            if first.value().name() != "td"
                || first.value().attr("colspan") != Some("10")
                || children.next().is_some()
            {
                return Err(invalid_page("malformed mobile detail row"));
            }
            continue;
        }
        let mobile_instructor = row
            .value()
            .classes()
            .any(|class| class == "mobileInstructorRow");
        if mobile_instructor || row.value().classes().any(|class| class == "otherRow") {
            // Continuation meetings and mobile duplicates do not define new sections.
            // Mobile instructor rows have an extra cell when TAs are displayed.
            let cell_count = children.try_fold(1, |count, cell| {
                (count < 10 && cell.value().name() == "td").then_some(count + 1)
            });
            let valid_cell_count =
                cell_count == Some(10) || (mobile_instructor && cell_count == Some(9));
            if sections.is_empty()
                || first.value().name() != "td"
                || first.text().any(|part| !part.trim().is_empty())
                || !valid_cell_count
            {
                return Err(invalid_page("malformed supplemental section row"));
            }
            continue;
        }
        let mut cells = [first; 10];
        for cell in &mut cells[1..] {
            *cell = children
                .next()
                .ok_or_else(|| invalid_page("incomplete section row"))?;
        }
        if children.next().is_some() || cells.iter().any(|c| c.value().name() != "td") {
            return Err(invalid_page("malformed section row"));
        }
        if !row.value().classes().any(|class| class == "newsect")
            || !row.value().classes().any(|class| class == "mainRow")
        {
            return Err(invalid_page("unclassified section table row"));
        }
        let label = text(cells[0]);
        let (section, registration) = label
            .split_once(" (")
            .ok_or_else(|| invalid_page("malformed section label"))?;
        if section.is_empty()
            || !section.as_bytes()[0].is_ascii_uppercase()
            || !section
                .bytes()
                .all(|b| b.is_ascii_uppercase() || b.is_ascii_digit())
            || registration
                .strip_suffix(')')
                .is_none_or(|r| r.is_empty() || !r.bytes().all(|b| b.is_ascii_digit()))
            || sections.iter().any(|existing: &String| existing == section)
        {
            return Err(invalid_page("invalid or duplicate section identifier"));
        }
        budget.charge(&[size_of::<String>(), section.len()])?;
        let names = if cells[4].select(&selectors.ta).next().is_some() {
            cells[4].select(&selectors.ta)
        } else {
            cells[3].select(&selectors.instructor)
        };
        for name in names.map(text) {
            if name.is_empty() {
                return Err(invalid_page("empty instructor or TA name"));
            }
            // Charge every association before cloning, even for repeated names.
            budget.charge(&[
                size_of::<(String, Vec<String>)>(),
                name.len(),
                size_of::<String>(),
                section.len(),
            ])?;
            instructors
                .entry(name)
                .or_default()
                .push(section.to_owned());
        }
        sections.push(section.to_owned());
    }
    if element.select(&selectors.rows).count() != sections.len()
        || table.select(&selectors.main_rows).count() != sections.len()
    {
        return Err(invalid_page(
            "section outside expected table or missing section marker",
        ));
    }
    Ok(Course {
        code,
        title: title.to_owned(),
        credits,
        instructors,
        sections: (!sections.is_empty()).then_some(sections),
    })
}
